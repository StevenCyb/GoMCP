package caller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	headerVersion = "Mcp-Protocol-Version"
	headerMethod  = "Mcp-Method"
	headerName    = "Mcp-Name"
	headerParam   = "Mcp-Param-"
	sseData       = "data:"
	scopeError    = "insufficient_scope"
)

// AuthError is a server that wants credentials the client does not have.
type AuthError struct {
	Status    int
	Challenge string // WWW-Authenticate of the response
	Hint      string
}

func (e *AuthError) Error() string {
	msg := fmt.Sprintf("the server answered %d", e.Status)
	if e.Challenge != "" {
		msg += " (" + e.Challenge + ")"
	}
	return msg + ": " + e.Hint
}

// HTTP is the Streamable HTTP transport of the stateless protocol: one POST per request.
type HTTP struct {
	URL    string
	Client *http.Client
	Auth   *Auth           // nil sends no credentials
	ctx    context.Context //nolint:containedctx // RoundTrip has no context parameter
}

// NewHTTP returns a transport for the MCP endpoint at url.
func NewHTTP(ctx context.Context, url string, auth *Auth) *HTTP {
	if auth != nil {
		auth.server = url
	}
	return &HTTP{URL: url, Client: &http.Client{Transport: ipv4First()}, Auth: auth, ctx: ctx}
}

// RoundTrip posts the request. A 401 or an insufficient_scope 403 is answered with a login when OAuth
// is on, then the request is sent once more.
func (h *HTTP) RoundTrip(r Round) (json.RawMessage, error) {
	res, err := h.post(r)
	if err != nil {
		return nil, err
	}
	challenge := res.Header.Get("WWW-Authenticate")
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden && strings.Contains(challenge, scopeError) {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if err := h.Auth.answer(h.ctx, h.Client, h.URL, res.StatusCode, challenge); err != nil {
			return nil, err
		}
		if res, err = h.post(r); err != nil {
			return nil, err
		}
	}
	defer func() { _ = res.Body.Close() }()
	return readResponse(res, r.ID)
}

// Login runs the OAuth flow now, even if a token is cached.
func (h *HTTP) Login() error {
	if h.Auth == nil || !h.Auth.OAuth {
		return errors.New("login needs --oauth")
	}
	return h.Auth.login(h.ctx, h.Client, h.URL, "", "")
}

func (h *HTTP) wantsHeaders() bool { return true }

func (h *HTTP) post(r Round) (*http.Response, error) {
	req, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(headerVersion, specVersion)
	req.Header.Set(headerMethod, r.Method)
	if r.Name != "" {
		req.Header.Set(headerName, encodeHeader(r.Name))
	}
	for k, v := range r.Headers {
		req.Header.Set(headerParam+k, encodeHeader(v))
	}
	if tok := h.Auth.bearer(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	return res, nil
}

// encodeHeader sends a value that is not printable ASCII as =?base64?...?=.
func encodeHeader(v string) string {
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(v)) + "?="
		}
	}
	return v
}

func readResponse(res *http.Response, id int) (json.RawMessage, error) {
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		hint := "pass --token <bearer> or --oauth"
		if res.StatusCode == http.StatusForbidden {
			hint = "the token lacks a permission, try --oauth --scope <scope>"
		}
		return nil, &AuthError{Status: res.StatusCode, Challenge: res.Header.Get("WWW-Authenticate"), Hint: hint}
	}
	var payload []byte
	var err error
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		payload, err = lastEvent(res.Body, id)
	} else {
		payload, err = io.ReadAll(io.LimitReader(res.Body, 16<<20))
	}
	if err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("the server answered %s with something that is not JSON: %w", res.Status, err)
	}
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Result, nil
}

// lastEvent returns the response to the request out of an SSE stream; notifications are skipped.
func lastEvent(r io.Reader, id int) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	want := fmt.Sprint(id)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), sseData)
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		var m message
		if json.Unmarshal([]byte(data), &m) == nil && m.Method == "" && string(m.ID) == want {
			return []byte(data), nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return nil, errors.New("the stream ended without a response")
}

// paramHeaders returns the Mcp-Param-* headers a tool call must carry: the arguments whose property
// has an x-mcp-header. On a stdio transport there are none.
func (c *Client) paramHeaders(params map[string]any) map[string]string {
	if w, ok := c.t.(interface{ wantsHeaders() bool }); !ok || !w.wantsHeaders() {
		return nil
	}
	name, _ := params["name"].(string)
	if c.tools == nil {
		_, _ = c.Tools()
	}
	var args map[string]json.RawMessage
	switch a := params["arguments"].(type) {
	case json.RawMessage:
		_ = json.Unmarshal(a, &args)
	case map[string]any:
		raw, _ := json.Marshal(a) //nolint:errchkjson // arguments are decoded JSON
		_ = json.Unmarshal(raw, &args)
	}
	out := map[string]string{}
	for _, t := range c.tools {
		if t.Name != name {
			continue
		}
		for prop, raw := range t.InputSchema.Properties {
			var p struct {
				Type   string `json:"type"`
				Header string `json:"x-mcp-header"` //nolint:tagliatelle // keyword defined by the MCP specification
			}
			v, ok := args[prop]
			if json.Unmarshal(raw, &p) != nil || p.Header == "" || !ok || string(v) == "null" {
				continue
			}
			if p.Type == "string" {
				var s string
				_ = json.Unmarshal(v, &s)
				out[p.Header] = s
			} else {
				out[p.Header] = strings.TrimSpace(string(v))
			}
		}
	}
	return out
}

// ipv4First dials IPv4 before IPv6. Some networks have a broken IPv6 route that accepts the connection
// and then fails the first write, which the dual stack fallback of Go does not see.
func ipv4First() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the default transport is an *http.Transport
	var d net.Dialer
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network == "tcp" {
			if c, err := d.DialContext(ctx, "tcp4", addr); err == nil {
				return c, nil
			}
		}
		return d.DialContext(ctx, network, addr)
	}
	return t
}
