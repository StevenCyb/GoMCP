package caller_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokenWriter = "writer"
	toolWhoami  = "whoami"
	toolRegion  = "region"
	codeGranted = "granted"
)

type regionIn struct {
	Region string `header:"Region" json:"region"`
}

// remote is an MCP server that wants a token, with an authorization server next to it.
type remote struct {
	srv       *httptest.Server
	authorize atomic.Int32 // logins done
	register  atomic.Int32 // clients registered
	scopes    atomic.Value // scope of the last login
	challenge string       // PKCE challenge of the last login
}

func newRemote(t *testing.T) *remote {
	t.Helper()
	r := &remote{}
	mux := http.NewServeMux()
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)

	s := mcp.NewServer(mcp.Info{Name: "remote", Version: "1"}, mcp.WithAuth(mcp.AuthScheme{
		Type: mcp.AuthOAuth, Resource: r.srv.URL + "/mcp", AuthorizationServers: []string{r.srv.URL},
		Scopes: []mcp.ScopeDef{{Name: "read", Default: true}},
		Verify: func(_ context.Context, token string) (mcp.Principal, error) {
			if token == tokenWriter {
				return mcp.Principal{Subject: "bob", Scopes: []string{"read"}}, nil
			}
			return mcp.Principal{}, errors.New("unknown token")
		},
	}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: toolWhoami},
		func(ctx mcp.Context, _ mcp.Empty) (string, error) { return ctx.Principal().Subject, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: toolRegion},
		func(_ mcp.Context, in regionIn) (string, error) { return "region " + in.Region, nil }))
	s.Mount(mux, "/mcp")

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": r.srv.URL + "/authorize", "token_endpoint": r.srv.URL + "/token",
			"registration_endpoint": r.srv.URL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		r.register.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "registered"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		r.authorize.Add(1)
		r.scopes.Store(q.Get("scope"))
		r.challenge = q.Get("code_challenge")
		back := q.Get("redirect_uri") + "?code=" + codeGranted + "&state=" + url.QueryEscape(q.Get("state"))
		http.Redirect(w, req, back, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		sum := sha256.Sum256([]byte(req.PostForm.Get("code_verifier")))
		if req.PostForm.Get("code") != codeGranted || base64.RawURLEncoding.EncodeToString(sum[:]) != r.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tokenWriter, "expires_in": 3600})
	})
	return r
}

func (r *remote) client(t *testing.T, auth *caller.Auth) *caller.Client {
	t.Helper()
	return caller.NewClient(caller.NewHTTP(t.Context(), r.srv.URL+"/mcp", auth), nil)
}

// browser plays the user: it opens the login page, which redirects to the loopback address.
func browser(t *testing.T, opened *atomic.Int32) func(string) error {
	t.Helper()
	return func(u string) error {
		opened.Add(1)
		res, err := http.Get(u) //nolint:noctx // a test calls the fake authorization server
		if err == nil {
			_ = res.Body.Close()
		}
		return err //nolint:wrapcheck // a test
	}
}

func TestHTTPBearerToken(t *testing.T) {
	r := newRemote(t)
	raw, err := r.client(t, &caller.Auth{Token: tokenWriter}).Call(toolWhoami, nil)
	require.NoError(t, err)
	assert.Equal(t, "bob", caller.Text(raw))
}

func TestHTTPWithoutOrWithWrongToken(t *testing.T) {
	r := newRemote(t)
	var authErr *caller.AuthError
	_, err := r.client(t, nil).Tools()
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, http.StatusUnauthorized, authErr.Status)
	assert.Contains(t, err.Error(), "--oauth")

	_, err = r.client(t, &caller.Auth{Token: "nope"}).Tools()
	require.ErrorAs(t, err, &authErr)
	assert.Contains(t, err.Error(), "rejected the token")
}

func TestHTTPOAuthLogsInOnceAndCachesTheToken(t *testing.T) {
	r := newRemote(t)
	cache := filepath.Join(t.TempDir(), "gomcp", "tokens.json")
	var opened atomic.Int32
	auth := func() *caller.Auth {
		return &caller.Auth{OAuth: true, Open: browser(t, &opened), CacheFile: cache}
	}

	raw, err := r.client(t, auth()).Call(toolWhoami, nil)
	require.NoError(t, err, "the 401 starts the login and the call is sent again")
	assert.Equal(t, "bob", caller.Text(raw))
	assert.EqualValues(t, 1, opened.Load())
	assert.EqualValues(t, 1, r.register.Load(), "no client id: the client registers itself")
	assert.Equal(t, "read", r.scopes.Load(), "the default scope of the server")

	raw, err = r.client(t, auth()).Call(toolWhoami, nil)
	require.NoError(t, err)
	assert.Equal(t, "bob", caller.Text(raw))
	assert.EqualValues(t, 1, opened.Load(), "the cached token is used, no second login")
}

func TestHTTPOAuthWithClientIDAndScope(t *testing.T) {
	r := newRemote(t)
	var opened atomic.Int32
	c := r.client(t, &caller.Auth{OAuth: true, ClientID: "mine", Scope: "a b", Open: browser(t, &opened), CacheFile: filepath.Join(t.TempDir(), "t.json")})
	_, err := c.Tools()
	require.NoError(t, err)
	assert.EqualValues(t, 0, r.register.Load(), "a client id means no registration")
	assert.Equal(t, "a b", r.scopes.Load())
}

func TestHTTPLoginAgain(t *testing.T) {
	r := newRemote(t)
	var opened atomic.Int32
	c := r.client(t, &caller.Auth{OAuth: true, Open: browser(t, &opened), CacheFile: filepath.Join(t.TempDir(), "t.json")})
	require.NoError(t, c.Login())
	require.NoError(t, c.Login())
	assert.EqualValues(t, 2, opened.Load(), "login always opens the browser")
	_, err := c.Tools()
	require.NoError(t, err)
}

func TestHTTPParamHeaders(t *testing.T) {
	r := newRemote(t)
	raw, err := r.client(t, &caller.Auth{Token: tokenWriter}).Call(toolRegion, json.RawMessage(`{"region":"eu-west"}`))
	require.NoError(t, err, "the Mcp-Param-Region header is sent without the caller asking")
	assert.Equal(t, "region eu-west", caller.Text(raw))
}

func TestLoginOnStdioIsRefused(t *testing.T) {
	assert.Error(t, client(t).Login())
}
