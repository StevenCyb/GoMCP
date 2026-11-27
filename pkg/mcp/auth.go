package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/transport"
)

// AuthType selects how a scheme challenges clients.
type AuthType string

// Auth types.
const (
	// AuthBearer only runs the verifier; failures get a plain 401 (API keys, internal tokens).
	AuthBearer AuthType = "bearer"
	// AuthOAuth makes the server an OAuth 2.1 resource server: protected resource metadata,
	// WWW-Authenticate challenges, scope step-up.
	AuthOAuth AuthType = "oauth"
)

// Verifier validates a bearer token and returns the caller.
type Verifier func(ctx context.Context, token string) (Principal, error)

// ScopeDef declares a scope of an OAuth scheme.
type ScopeDef struct {
	Name        string
	Description string
	// Default scopes are requested in the WWW-Authenticate challenge.
	Default bool
}

// AuthScheme is one way to authenticate; several schemes are tried in order, first success wins.
type AuthScheme struct {
	Type   AuthType
	Verify Verifier
	// Resource is the canonical URI of this server (OAuth only).
	Resource string
	// AuthorizationServers are the issuers of accepted tokens (OAuth only).
	AuthorizationServers []string
	Scopes               []ScopeDef
}

// WithAuth turns on authentication; every request needs a bearer token one scheme accepts.
func WithAuth(schemes ...AuthScheme) Option {
	return func(o *options) { o.auth = append(o.auth, schemes...) }
}

const wellKnownPRM = "/.well-known/oauth-protected-resource"

// oauthScheme returns the first OAuth scheme.
func (s *Server) oauthScheme() (AuthScheme, bool) {
	for _, sc := range s.opts.auth {
		if sc.Type == AuthOAuth {
			return sc, true
		}
	}
	return AuthScheme{}, false
}

// metadataURL is where clients find the protected resource metadata of the scheme.
func (sc AuthScheme) metadataURL() string {
	u, err := url.Parse(sc.Resource)
	if err != nil || u.Host == "" {
		return ""
	}
	path := strings.TrimSuffix(u.Path, "/")
	return u.Scheme + "://" + u.Host + wellKnownPRM + path
}

func (sc AuthScheme) defaultScopes() []string {
	var out []string
	for _, def := range sc.Scopes {
		if def.Default {
			out = append(out, def.Name)
		}
	}
	return out
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func (s *Server) authenticate(r *http.Request) (any, *transport.AuthFailure) {
	token, ok := bearerToken(r)
	if !ok {
		return nil, s.unauthorized("")
	}
	for _, sc := range s.opts.auth {
		if sc.Verify == nil {
			continue
		}
		if p, err := sc.Verify(r.Context(), token); err == nil {
			return p, nil
		}
	}
	return nil, s.unauthorized("invalid_token")
}

func quoted(key, value string) string { return key + `="` + value + `"` }

// unauthorized builds the 401 challenge; errCode is empty if no token was sent.
func (s *Server) unauthorized(errCode string) *transport.AuthFailure {
	var params []string
	if errCode != "" {
		params = append(params, quoted("error", errCode))
	}
	if sc, ok := s.oauthScheme(); ok {
		if meta := sc.metadataURL(); meta != "" {
			params = append(params, quoted("resource_metadata", meta))
		}
		if scopes := sc.defaultScopes(); len(scopes) > 0 {
			params = append(params, quoted("scope", strings.Join(scopes, " ")))
		}
	}
	return &transport.AuthFailure{Status: http.StatusUnauthorized, Challenge: challenge(params)}
}

// forbidden builds the 403 challenge that asks the client for more scopes.
// For a group of alternatives it names the first one.
func (s *Server) forbidden(missing [][]string) *transport.AuthFailure {
	need := make([]string, len(missing))
	for i, group := range missing {
		need[i] = group[0]
	}
	params := []string{quoted("error", "insufficient_scope"), quoted("scope", strings.Join(need, " "))}
	if sc, ok := s.oauthScheme(); ok {
		if meta := sc.metadataURL(); meta != "" {
			params = append(params, quoted("resource_metadata", meta))
		}
	}
	return &transport.AuthFailure{Status: http.StatusForbidden, Challenge: challenge(params)}
}

func challenge(params []string) string {
	if len(params) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(params, ", ")
}

// requiredScopes returns the scopes a request needs.
//
// Each method reads only the field its handler reads. Decoding the whole body into one struct would let
// a wrongly typed unrelated field fail the decode here while the handler still accepts the request.
func (s *Server) requiredScopes(method string, params json.RawMessage) [][]string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(params, &fields) != nil {
		return nil
	}
	str := func(raw json.RawMessage, key string) string {
		var m map[string]json.RawMessage
		if raw != nil {
			_ = json.Unmarshal(raw, &m)
		}
		var v string
		_ = json.Unmarshal(m[key], &v)
		return v
	}
	var name, uri string
	_ = json.Unmarshal(fields["name"], &name)
	_ = json.Unmarshal(fields["uri"], &uri)
	switch method {
	case "tools/call":
		return s.toolScopes(name)
	case "prompts/get":
		return s.promptScopes(name)
	case "resources/read":
		return s.resourceScopes(uri)
	case "completion/complete":
		switch ref := fields["ref"]; str(ref, "type") {
		case "ref/prompt":
			return s.promptScopes(str(ref, "name"))
		case "ref/resource":
			return s.resourceScopes(str(ref, "uri"))
		}
	}
	return nil
}

func (s *Server) toolScopes(name string) [][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t := s.toolIdx[name]; t != nil {
		return t.def.Scopes
	}
	return nil
}

func (s *Server) promptScopes(name string) [][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.promptIdx[name]; p != nil {
		return p.def.Scopes
	}
	return nil
}

func (s *Server) resourceScopes(uri string) [][]string {
	if r, _ := s.findResource(uri); r != nil {
		return r.def.Scopes
	}
	return nil
}

// missingScopes returns the groups the principal does not satisfy. A group is satisfied by
// any one of its scopes (OR); all groups are required (AND).
func missingScopes(p Principal, need [][]string) [][]string {
	var missing [][]string
	for _, group := range need {
		if !slices.ContainsFunc(group, func(scope string) bool { return slices.Contains(p.Scopes, scope) }) {
			missing = append(missing, group)
		}
	}
	return missing
}

func hasScopes(p Principal, need [][]string) bool { return len(missingScopes(p, need)) == 0 }

func (s *Server) authorize(c *transport.Call, method string, params json.RawMessage) *transport.AuthFailure {
	need := s.requiredScopes(method, params)
	if len(need) == 0 {
		return nil
	}
	principal, _ := c.Principal().(Principal)
	if missing := missingScopes(principal, need); len(missing) > 0 {
		return s.forbidden(missing)
	}
	return nil
}

// MetadataHandler serves the OAuth protected resource metadata (RFC 9728); it answers 404
// when no OAuth scheme is configured.
func (s *Server) MetadataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.oauthScheme()
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		scopes := make([]string, 0, len(sc.Scopes))
		for _, def := range sc.Scopes {
			scopes = append(scopes, def.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":                 sc.Resource,
			"authorization_servers":    sc.AuthorizationServers,
			"scopes_supported":         scopes,
			"bearer_methods_supported": []string{"header"},
		})
	})
}

// Mount registers the MCP endpoint and, for OAuth, the protected resource metadata
// (path-inserted and at the well-known root).
func (s *Server) Mount(mux *http.ServeMux, endpoint string) {
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	mux.Handle(endpoint, s)
	if _, ok := s.oauthScheme(); ok {
		meta := s.MetadataHandler()
		mux.Handle(wellKnownPRM+endpoint, meta)
		mux.Handle(wellKnownPRM, meta)
	}
}
