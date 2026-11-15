package parser

import (
	"go/ast"
	"go/types"

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
			def.Description, _ = ann.String("description")
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
