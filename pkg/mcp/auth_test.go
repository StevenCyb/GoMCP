package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scopeRead  = "notes:read"
	scopeWrite = "notes:write"
	resourceID = "https://notes.example.com/mcp"
	metaURL    = "https://notes.example.com/.well-known/oauth-protected-resource/mcp"
)

var tokens = map[string]mcp.Principal{
	"reader": {Subject: "alice", Scopes: []string{scopeRead}},
	"writer": {Subject: "bob", Scopes: []string{scopeRead, scopeWrite}},
}

func verifyTokens(_ context.Context, token string) (mcp.Principal, error) {
	if p, ok := tokens[token]; ok {
		return p, nil
	}
	return mcp.Principal{}, errors.New("unknown token")
}

func oauthScheme() mcp.AuthScheme {
	return mcp.AuthScheme{
		Type:                 mcp.AuthOAuth,
		Verify:               verifyTokens,
		Resource:             resourceID,
		AuthorizationServers: []string{"https://auth.example.com"},
		Scopes: []mcp.ScopeDef{
			{Name: scopeRead, Description: "Read notes", Default: true},
			{Name: scopeWrite, Description: "Write notes"},
		},
	}
}

func authedServer(t *testing.T, schemes ...mcp.AuthScheme) *mcp.Server {
	t.Helper()
	s := newBasicServer(mcp.WithAuth(schemes...))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "public"},
		func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "write_note", Scopes: [][]string{{scopeWrite}}},
		func(mcp.Context, mcp.Empty) (string, error) { return "written", nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "whoami"},
		func(ctx mcp.Context, _ mcp.Empty) (string, error) { return ctx.Principal().Subject, nil }))
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate, Name: "n", Scopes: [][]string{{scopeWrite}}},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("x")}, nil }))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "p", Scopes: [][]string{{scopeWrite}}},
		func(mcp.Context, mcp.Empty) (mcp.Messages, error) { return mcp.Messages{mcp.UserText("hi")}, nil }))
	return s
}

// status sends a request and returns the HTTP status and WWW-Authenticate header.
func status(t *testing.T, c *testClient, method string, params map[string]any) (int, string) {
	t.Helper()
	resp := c.post(method, params, nil)
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("WWW-Authenticate")
}

func TestNoAuthConfiguredAllowsEverything(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		return "[" + ctx.Principal().Subject + "]", nil
	}))
	c := connect(t, newServer(t, s), nil)
	assert.Equal(t, "[]", firstText(t, callTool(t, c, "t", nil)))
}

func TestOAuthChallenges(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))

	code, header := status(t, connect(t, srv, nil), "tools/list", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, `Bearer resource_metadata="`+metaURL+`", scope="`+scopeRead+`"`, header)

	code, header = status(t, connectAs(t, srv, "nope", nil), "tools/list", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, `Bearer error="invalid_token", resource_metadata="`+metaURL+`", scope="`+scopeRead+`"`, header)
}

func TestTokenMustBeABearerHeader(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))
	c := connect(t, srv, nil)

	resp := c.post("tools/list", nil, map[string]string{"Authorization": "Basic d3JpdGVy"})
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	c.url += "?access_token=writer"
	code, _ := status(t, c, "tools/list", nil)
	assert.Equal(t, http.StatusUnauthorized, code, "tokens in the query string are ignored")
}

func TestAuthenticatedCallSeesPrincipal(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))
	c := connectAs(t, srv, "writer", nil)
	assert.Equal(t, "bob", firstText(t, callTool(t, c, "whoami", nil)))
}

func TestScopesAreEnforced(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))
	reader, writer := connectAs(t, srv, "reader", nil), connectAs(t, srv, "writer", nil)
	insufficient := `Bearer error="insufficient_scope", scope="` + scopeWrite + `", resource_metadata="` + metaURL + `"`

	cases := map[string]struct {
		method string
		params map[string]any
	}{
		"tool":     {"tools/call", map[string]any{keyName: "write_note"}},
		"resource": {"resources/read", map[string]any{keyURI: "note:///x"}},
		"prompt":   {"prompts/get", map[string]any{keyName: "p"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			code, header := status(t, reader, tc.method, tc.params)
			assert.Equal(t, http.StatusForbidden, code)
			assert.Equal(t, insufficient, header)
			code, _ = status(t, writer, tc.method, tc.params)
			assert.Equal(t, http.StatusOK, code)
		})
	}
	assert.Equal(t, "ok", firstText(t, callTool(t, reader, "public", nil)), "unscoped features stay open")
}

func TestBearerScheme(t *testing.T) {
	srv := newServer(t, authedServer(t, mcp.AuthScheme{Type: mcp.AuthBearer, Verify: verifyTokens}))

	code, header := status(t, connect(t, srv, nil), "tools/list", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "Bearer", header, "no metadata and no scope for plain bearer")

	code, header = status(t, connectAs(t, srv, "reader", nil), "tools/call", map[string]any{keyName: "write_note"})
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, `Bearer error="insufficient_scope", scope="`+scopeWrite+`"`, header)
}

func TestSeveralSchemesFirstSuccessWins(t *testing.T) {
	apiKey := mcp.AuthScheme{Type: mcp.AuthBearer, Verify: func(_ context.Context, token string) (mcp.Principal, error) {
		if token == "key" {
			return mcp.Principal{Subject: "service", Scopes: []string{scopeRead, scopeWrite}}, nil
		}
		return mcp.Principal{}, errors.New("not an api key")
	}}
	srv := newServer(t, authedServer(t, apiKey, oauthScheme()))

	assert.Equal(t, "service", firstText(t, callTool(t, connectAs(t, srv, "key", nil), "whoami", nil)))
	assert.Equal(t, "bob", firstText(t, callTool(t, connectAs(t, srv, "writer", nil), "whoami", nil)))

	_, header := status(t, connect(t, srv, nil), "tools/list", nil)
	assert.Contains(t, header, "resource_metadata", "the OAuth scheme provides the challenge")
}

func TestMetadataHandler(t *testing.T) {
	s := authedServer(t, oauthScheme())
	rec := httptest.NewRecorder()
	s.MetadataHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var meta map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))
	assert.Equal(t, resourceID, meta["resource"])
	assert.Equal(t, []any{"https://auth.example.com"}, meta["authorization_servers"])
	assert.Equal(t, []any{scopeRead, scopeWrite}, meta["scopes_supported"])
	assert.Equal(t, []any{"header"}, meta["bearer_methods_supported"])

	rec = httptest.NewRecorder()
	newBasicServer().MetadataHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "no OAuth scheme, no metadata")
}

func TestMount(t *testing.T) {
	s := authedServer(t, oauthScheme())
	mux := http.NewServeMux()
	s.Mount(mux, "/mcp")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	get := func(path string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusOK, get("/.well-known/oauth-protected-resource/mcp"))
	assert.Equal(t, http.StatusOK, get("/.well-known/oauth-protected-resource"))

	c := connectAs(t, srv, "writer", nil)
	c.url = srv.URL + "/mcp"
	code, _ := status(t, c, "tools/list", nil)
	assert.Equal(t, http.StatusOK, code)
}

func TestMountWithoutOAuthOnlyServesTheEndpoint(t *testing.T) {
	s := newBasicServer()
	mux := http.NewServeMux()
	s.Mount(mux, "/mcp")
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { s.Close(); srv.Close() })

	c := connect(t, srv, nil)
	c.url = srv.URL + "/mcp"
	code, _ := status(t, c, "tools/list", nil)
	assert.Equal(t, http.StatusOK, code)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/.well-known/oauth-protected-resource", nil)
	require.NoError(t, err)
	meta, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = meta.Body.Close()
	assert.Equal(t, http.StatusNotFound, meta.StatusCode)
}

func TestScopesCannotBeBypassedByAMistypedExtraField(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))
	reader := connectAs(t, srv, "reader", nil)

	// the handler only reads its own field, so a wrong type in another one must not skip the scope check
	cases := map[string]struct {
		method string
		params map[string]any
	}{
		"tool":     {"tools/call", map[string]any{keyName: "write_note", keyURI: 5}},
		"resource": {"resources/read", map[string]any{keyURI: "note:///x", keyName: 5}},
		"prompt":   {"prompts/get", map[string]any{keyName: "p", keyURI: 5}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			code, _ := status(t, reader, tc.method, tc.params)
			assert.Equal(t, http.StatusForbidden, code)
		})
	}
}

func TestCompletionNeedsTheScopesOfItsTarget(t *testing.T) {
	srv := newServer(t, authedServer(t, oauthScheme()))
	reader, writer := connectAs(t, srv, "reader", nil), connectAs(t, srv, "writer", nil)

	for name, ref := range map[string]map[string]any{
		"prompt":   {"type": refPrompt, keyName: "p"},
		"resource": {"type": "ref/resource", keyURI: noteTemplate},
	} {
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"ref": ref, "argument": map[string]any{keyName: "id", "value": ""}}
			code, _ := status(t, reader, "completion/complete", params)
			assert.Equal(t, http.StatusForbidden, code)
			code, _ = status(t, writer, "completion/complete", params)
			assert.Equal(t, http.StatusOK, code)
		})
	}
}

func TestListeningOnlyAcknowledgesResourcesTheCallerMayRead(t *testing.T) {
	s := authedServer(t, oauthScheme())
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: "open:///{id}", Name: "o", Subscribable: true},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("x")}, nil }))
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: "secret:///{id}", Name: "s", Subscribable: true, Scopes: [][]string{{scopeWrite}}},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("x")}, nil }))
	srv := newServer(t, s)
	filter := map[string]any{"resourceSubscriptions": []string{"open:///1", "secret:///1"}}

	agreed := func(token string) string {
		ack := connectAs(t, srv, token, nil).listen(filter).next()
		return string(rpc.MustJSON(mustField(t, ack.Params, keyNotifications)))
	}
	assert.JSONEq(t, `{"resourceSubscriptions":["open:///1"]}`, agreed("reader"), "the reader does not get the scoped resource")
	assert.JSONEq(t, `{"resourceSubscriptions":["open:///1","secret:///1"]}`, agreed("writer"))
}

func TestScopeGroupsAreOrWithinAndAcrossLines(t *testing.T) {
	const scopeOps = "notes:ops"
	tokens["ops"] = mcp.Principal{Subject: "carol", Scopes: []string{scopeOps}}
	tokens["both"] = mcp.Principal{Subject: "dave", Scopes: []string{scopeRead, scopeOps}}
	t.Cleanup(func() { delete(tokens, "ops"); delete(tokens, "both") })

	s := newBasicServer(mcp.WithAuth(oauthScheme()))
	handler := func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil }
	// (write OR ops)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "either", Scopes: [][]string{{scopeWrite, scopeOps}}}, handler))
	// (read) AND (write OR ops)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "gated", Scopes: [][]string{{scopeRead}, {scopeWrite, scopeOps}}}, handler))
	srv := newServer(t, s)

	cases := []struct {
		tool, token string
		code        int
		scope       string
	}{
		{"either", "writer", http.StatusOK, ""},
		{"either", "ops", http.StatusOK, ""},
		{"either", "reader", http.StatusForbidden, scopeWrite},
		{"gated", "both", http.StatusOK, ""},
		{"gated", "writer", http.StatusOK, ""},
		{"gated", "ops", http.StatusForbidden, scopeRead},
		{"gated", "reader", http.StatusForbidden, scopeWrite},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.token, func(t *testing.T) {
			code, header := status(t, connectAs(t, srv, tc.token, nil), "tools/call", map[string]any{keyName: tc.tool})
			assert.Equal(t, tc.code, code)
			if tc.scope != "" {
				assert.Contains(t, header, `scope="`+tc.scope+`"`, "challenge names the first alternative of each missing group")
			}
		})
	}
}
