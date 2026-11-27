package caller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginTimeout = 5 * time.Minute
	expirySkew   = 30 * time.Second
	maxMetadata  = 1 << 20
)

// Auth is how the client signs its requests: a fixed bearer token, or OAuth with the browser.
// A nil *Auth sends no credentials.
type Auth struct {
	Token    string // fixed bearer token
	OAuth    bool   // log in with the browser when the server asks for credentials
	ClientID string // OAuth client; empty registers one dynamically
	Scope    string // OAuth scope; empty takes what the server names
	// CallbackPort is the port of the redirect address, for authorization servers that only accept a registered one; 0 picks a free port.
	CallbackPort int
	// Open shows the login page to the user; nil opens the system browser.
	Open func(loginURL string) error
	// Log gets the login URL, for a machine without a browser; nil prints nothing.
	Log io.Writer
	// CacheFile stores the tokens between runs; empty means gomcp/tokens.json in the user config directory.
	CacheFile string

	mu     sync.Mutex
	server string
	cur    *tokenEntry
	loaded bool
}

// tokenEntry is what is cached per server.
type tokenEntry struct {
	Access        string    `json:"access_token"`
	Refresh       string    `json:"refresh_token,omitempty"`
	Expiry        time.Time `json:"expiry,omitzero"`
	ClientID      string    `json:"client_id"`
	TokenEndpoint string    `json:"token_endpoint"`
	Scope         string    `json:"scope,omitempty"`
}

// bearer returns the token to send, or "".
func (a *Auth) bearer() string {
	if a == nil {
		return ""
	}
	if a.Token != "" {
		return a.Token
	}
	if !a.OAuth {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.loaded {
		a.loaded = true
		a.cur = a.readCache()[a.server]
	}
	if a.cur == nil || !a.cur.Expiry.IsZero() && time.Now().After(a.cur.Expiry.Add(-expirySkew)) {
		return "" // the server answers 401, which refreshes it
	}
	return a.cur.Access
}

// answer reacts to a 401 or an insufficient_scope 403: it refreshes or logs in, or says what is missing.
func (a *Auth) answer(ctx context.Context, hc *http.Client, server string, status int, challenge string) error {
	if a == nil || a.Token == "" && !a.OAuth {
		return &AuthError{Status: status, Challenge: challenge, Hint: "pass --token <bearer> or --oauth"}
	}
	if a.Token != "" {
		return &AuthError{Status: status, Challenge: challenge, Hint: "the server rejected the token"}
	}
	if status == http.StatusUnauthorized {
		a.mu.Lock()
		cur := a.cur
		a.mu.Unlock()
		if cur != nil && cur.Refresh != "" && a.refresh(ctx, hc, server, cur) == nil {
			return nil
		}
	}
	return a.login(ctx, hc, server, challengeParam(challenge, "scope"), challenge)
}

// login runs authorization code + PKCE with the browser and caches the token.
func (a *Auth) login(ctx context.Context, hc *http.Client, server, scope, challenge string) error {
	meta, err := discover(ctx, hc, server, challengeParam(challenge, "resource_metadata"))
	if err != nil {
		return err
	}
	if a.Scope != "" {
		scope = a.Scope
	} else if scope == "" {
		scope = strings.Join(meta.Scopes, " ")
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort("localhost", strconv.Itoa(a.CallbackPort)))
	if err != nil {
		return fmt.Errorf("listen for the login redirect: %w", err)
	}
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	redirect := "http://localhost:" + port + "/callback" // localhost: what authorization servers such as Entra register

	clientID := a.ClientID
	if clientID == "" {
		a.mu.Lock()
		if a.cur != nil {
			clientID = a.cur.ClientID
		}
		a.mu.Unlock()
	}
	if clientID == "" {
		if clientID, err = register(ctx, hc, meta, redirect); err != nil {
			return err
		}
	}

	verifier, state := randomString(), randomString()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	if !strings.HasSuffix(meta.issuerHost, "microsoftonline.com") { // Entra does not take RFC 8707 resource next to scopes
		q.Set("resource", meta.Resource)
	}
	loginURL := meta.AuthEndpoint + queryJoin(meta.AuthEndpoint) + q.Encode()

	codeCh := make(chan callback, 1)
	srv := &http.Server{Handler: callbackHandler(state, codeCh), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	if a.Log != nil {
		_, _ = fmt.Fprintf(a.Log, "Open this page to log in:\n%s\n", loginURL)
	}
	open := a.Open
	if open == nil {
		open = OpenBrowser
	}
	if err := open(loginURL); err != nil && a.Log != nil {
		_, _ = fmt.Fprintf(a.Log, "Could not open the browser: %v\n", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	var cb callback
	select {
	case cb = <-codeCh:
	case <-waitCtx.Done():
		return errors.New("login timed out: no answer from the browser")
	}
	if cb.err != nil {
		return cb.err
	}

	entry, err := exchange(ctx, hc, meta.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {cb.code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier},
	})
	if err != nil {
		return err
	}
	entry.ClientID, entry.TokenEndpoint, entry.Scope = clientID, meta.TokenEndpoint, scope
	a.store(server, entry)
	return nil
}

// refresh gets a new access token with the refresh token.
func (a *Auth) refresh(ctx context.Context, hc *http.Client, server string, cur *tokenEntry) error {
	entry, err := exchange(ctx, hc, cur.TokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {cur.Refresh}, "client_id": {cur.ClientID},
	})
	if err != nil {
		return err
	}
	if entry.Refresh == "" {
		entry.Refresh = cur.Refresh
	}
	entry.ClientID, entry.TokenEndpoint, entry.Scope = cur.ClientID, cur.TokenEndpoint, cur.Scope
	a.store(server, entry)
	return nil
}

func (a *Auth) store(server string, e *tokenEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cur, a.loaded = e, true
	all := a.readCache()
	all[server] = e
	a.writeCache(all)
}

func (a *Auth) cachePath() string {
	if a.CacheFile != "" {
		return a.CacheFile
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gomcp", "tokens.json")
}

func (a *Auth) readCache() map[string]*tokenEntry {
	all := map[string]*tokenEntry{}
	if p := a.cachePath(); p != "" {
		if raw, err := os.ReadFile(p); err == nil { //nolint:gosec // the token cache path
			_ = json.Unmarshal(raw, &all)
		}
	}
	return all
}

// writeCache is best effort: without a cache the user logs in again next time.
func (a *Auth) writeCache(all map[string]*tokenEntry) {
	p := a.cachePath()
	if p == "" {
		return
	}
	raw, err := json.MarshalIndent(all, "", "  ") //nolint:gosec // the cache holds tokens on purpose, the file is 0600
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	_ = os.WriteFile(p, raw, 0o600)
}

type callback struct {
	code string
	err  error
}

func callbackHandler(state string, out chan<- callback) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var cb callback
		switch {
		case r.URL.Path != "/callback":
			http.NotFound(w, r)
			return
		case q.Get("error") != "":
			cb.err = fmt.Errorf("login failed: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("state") != state:
			cb.err = errors.New("login failed: the state of the answer does not match")
		case q.Get("code") == "":
			cb.err = errors.New("login failed: no code in the answer")
		default:
			cb.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if cb.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<p>Login failed. You can close this tab.</p>")
		} else {
			_, _ = io.WriteString(w, "<p>Logged in. You can close this tab.</p>")
		}
		select {
		case out <- cb:
		default:
		}
	})
}

// serverMeta is what discovery finds out about a protected server.
type serverMeta struct {
	Resource, AuthEndpoint, TokenEndpoint, RegistrationEndpoint string
	Scopes                                                      []string
	issuerHost                                                  string
}

// discover reads the protected resource metadata (RFC 9728) and the authorization server metadata
// (RFC 8414 or OpenID Connect discovery).
func discover(ctx context.Context, hc *http.Client, server, prmURL string) (*serverMeta, error) {
	u, err := url.Parse(server)
	if err != nil {
		return nil, fmt.Errorf("server URL: %w", err)
	}
	origin := u.Scheme + "://" + u.Host
	var prm struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	candidates := []string{prmURL}
	if u.Path != "" && u.Path != "/" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+u.Path)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	var found bool
	for _, c := range candidates {
		if c != "" && getJSON(ctx, hc, c, &prm) == nil && len(prm.Servers) > 0 {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("login: the server does not publish OAuth metadata (oauth-protected-resource), use --token")
	}
	issuer, err := url.Parse(prm.Servers[0])
	if err != nil {
		return nil, fmt.Errorf("authorization server URL: %w", err)
	}
	base, path := issuer.Scheme+"://"+issuer.Host, strings.TrimSuffix(issuer.Path, "/")
	var errs []error
	var as struct {
		Auth  string `json:"authorization_endpoint"`
		Token string `json:"token_endpoint"`
		Reg   string `json:"registration_endpoint"`
	}
	for _, c := range []string{
		base + "/.well-known/oauth-authorization-server" + path,
		base + "/.well-known/openid-configuration" + path,
		strings.TrimSuffix(prm.Servers[0], "/") + "/.well-known/openid-configuration",
	} {
		err := getJSON(ctx, hc, c, &as)
		if err == nil && (as.Auth == "" || as.Token == "") {
			err = fmt.Errorf("get %s: no authorization_endpoint and token_endpoint in the answer", c)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resource := prm.Resource
		if resource == "" {
			resource = server
		}
		return &serverMeta{Resource: resource, AuthEndpoint: as.Auth, TokenEndpoint: as.Token,
			RegistrationEndpoint: as.Reg, Scopes: prm.Scopes, issuerHost: issuer.Hostname()}, nil
	}
	return nil, fmt.Errorf("login: no metadata found for the authorization server %s: %w", prm.Servers[0], errors.Join(errs...))
}

func getJSON(ctx context.Context, hc *http.Client, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", u, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxMetadata)).Decode(into)
}

// register registers a public client for the loopback redirect (RFC 7591).
func register(ctx context.Context, hc *http.Client, m *serverMeta, redirect string) (string, error) {
	if m.RegistrationEndpoint == "" {
		return "", errors.New("login: the authorization server cannot register clients, pass --client-id")
	}
	body, _ := json.Marshal(map[string]any{ //nolint:errchkjson // plain strings
		"client_name": "gomcp", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("register client: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxMetadata)).Decode(&out); err != nil || out.ClientID == "" {
		return "", fmt.Errorf("register client: %s without a client_id, pass --client-id", res.Status)
	}
	return out.ClientID, nil
}

// exchange calls the token endpoint.
func exchange(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxMetadata)).Decode(&out); err != nil {
		return nil, fmt.Errorf("token request: %s is not JSON", res.Status)
	}
	if out.Access == "" {
		return nil, fmt.Errorf("token request failed: %s %s", out.Error, out.Desc)
	}
	e := &tokenEntry{Access: out.Access, Refresh: out.Refresh}
	if out.Expires > 0 {
		e.Expiry = time.Now().Add(time.Duration(out.Expires) * time.Second)
	}
	return e, nil
}

// challengeParam reads one key="value" parameter of a WWW-Authenticate header.
func challengeParam(challenge, key string) string {
	rest := challenge
	for {
		i := strings.Index(rest, key+"=")
		if i < 0 {
			return ""
		}
		if i > 0 && rest[i-1] != ' ' && rest[i-1] != ',' {
			rest = rest[i+len(key):]
			continue
		}
		v := rest[i+len(key)+1:]
		if strings.HasPrefix(v, `"`) {
			v = v[1:]
			end := strings.IndexByte(v, '"')
			if end < 0 {
				return ""
			}
			return v[:end]
		}
		end := strings.IndexAny(v, ", ")
		if end < 0 {
			return v
		}
		return v[:end]
	}
}

func queryJoin(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// OpenBrowser opens a page in the system browser.
func OpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", u) //nolint:gosec // opens the login URL
	case "windows":
		cmd = exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", u) //nolint:gosec // opens the login URL
	default:
		cmd = exec.CommandContext(context.Background(), "xdg-open", u) //nolint:gosec // opens the login URL
	}
	return cmd.Start() //nolint:wrapcheck // the caller prints the reason
}
