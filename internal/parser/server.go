package parser

import (
	"fmt"
	"go/ast"
	"go/types"
	"net/http"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

func (c *collector) setup(fd *ast.FuncDecl, pos project.Pos, ref project.FuncRef) {
	if fd.Type.Params.NumFields() != 0 {
		c.errorf(pos, "Setup must take no parameters")
		return
	}
	if n := fd.Type.Results.NumFields(); n > 1 || (n == 1 && types.ExprString(fd.Type.Results.List[0].Type) != "error") {
		c.errorf(pos, "Setup must return nothing or an error")
		return
	}
	c.p.Setup = append(c.p.Setup, project.Setup{FuncRef: ref, ReturnsError: fd.Type.Results.NumFields() == 1})
}

func (c *collector) auth(feature located, a annotated, fd *ast.FuncDecl, ref project.FuncRef) {
	typ, _ := feature.String("type")
	if typ != "bearer" && typ != "oauth" {
		c.errorf(feature.pos, "Auth type %q is not valid, use bearer or oauth", typ)
		return
	}
	if !c.checkShape(fd, ref.Pos, "verifier", 2, 2, "context.Context") {
		return
	}
	scheme := project.Auth{Type: typ, Verify: ref}
	scheme.Resource, _ = feature.String("resource")
	for _, ann := range a.anns {
		switch ann.Name {
		case "AuthServer":
			if server, ok := ann.Positional(); ok {
				scheme.AuthorizationServers = append(scheme.AuthorizationServers, server)
			}
		case "AuthScope":
			name, ok := ann.Positional()
			if !ok {
				c.errorf(ann.pos, "AuthScope needs a name")
				continue
			}
			def := project.ScopeDef{Name: name, Default: ann.Flag("default")}
			scheme.Scopes = append(scheme.Scopes, def)
		}
	}
	if typ == "oauth" && scheme.Resource == "" {
		c.errorf(feature.pos, "Auth type oauth needs resource=\"...\"")
		return
	}
	if typ == "bearer" && (len(scheme.AuthorizationServers) > 0 || len(scheme.Scopes) > 0) {
		c.errorf(feature.pos, "AuthServer and AuthScope are only valid for type oauth")
		return
	}
	c.p.Auth = append(c.p.Auth, scheme)
}

// httpHandler registers a plain HTTP handler: func(http.ResponseWriter, *http.Request).
func (c *collector) httpHandler(feature located, fd *ast.FuncDecl, ref project.FuncRef) {
	pattern, ok := feature.Positional()
	if !ok || pattern == "" {
		c.errorf(feature.pos, `HTTPHandler needs a ServeMux pattern such as "POST /hook"`)
		return
	}
	if err := registerRoute(http.NewServeMux(), pattern); err != nil {
		c.errorf(feature.pos, "HTTPHandler pattern %q is not valid: %v", pattern, err)
		return
	}
	if !isHandlerFunc(fd) {
		c.errorf(ref.Pos, "HTTPHandler must be func(http.ResponseWriter, *http.Request) without results")
		return
	}
	c.p.Handlers = append(c.p.Handlers, project.HTTPHandler{FuncRef: ref, Pattern: pattern})
}

// isHandlerFunc reports whether fd takes an http.ResponseWriter and a *http.Request and returns nothing.
func isHandlerFunc(fd *ast.FuncDecl) bool {
	if fd.Type.Params.NumFields() != 2 || fd.Type.Results.NumFields() != 0 {
		return false
	}
	var kinds []string
	for _, field := range fd.Type.Params.List {
		for range max(1, len(field.Names)) {
			kinds = append(kinds, types.ExprString(field.Type))
		}
	}
	return strings.HasSuffix(kinds[0], ".ResponseWriter") && strings.HasPrefix(kinds[1], "*") && strings.HasSuffix(kinds[1], ".Request")
}

// registerRoute registers pattern on mux; the mux panics for an invalid pattern or a conflict.
func registerRoute(mux *http.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	return nil
}

// checkRoutes reports handlers that clash with the MCP endpoint, the asset routes or each other.
func (c *collector) checkRoutes() {
	mux := http.NewServeMux()
	endpoint := c.p.Transport.Endpoint
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	_ = registerRoute(mux, endpoint)
	for _, a := range c.p.Assets {
		_ = registerRoute(mux, strings.TrimSuffix(a.Prefix, "/")+"/")
	}
	for _, h := range c.p.Handlers {
		if err := registerRoute(mux, h.Pattern); err != nil {
			c.errorf(h.Pos, "HTTPHandler %q conflicts with the MCP endpoint, an assets route or another handler", h.Pattern)
		}
	}
}
