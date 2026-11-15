package parser

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

// genDecl handles annotations on types and variables.
func (c *collector) genDecl(f *file, gd *ast.GenDecl) {
	groups := []*ast.CommentGroup{gd.Doc}
	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			groups = append(groups, s.Doc)
		case *ast.ValueSpec:
			groups = append(groups, s.Doc)
		}
	}
	for _, g := range groups {
		a := c.parseGroup(f, g)
		for _, ann := range a.anns {
			c.declAnnotation(f, gd, ann)
		}
	}
}

func (c *collector) declAnnotation(f *file, gd *ast.GenDecl, ann located) {
	switch ann.Name {
	case "Elicitation":
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				if _, isStruct := ts.Type.(*ast.StructType); isStruct {
					return
				}
			}
		}
		c.errorf(ann.pos, "Elicitation must be placed on a struct type")
	case "ServeAssets":
		prefix, ok := ann.Positional()
		if !ok || !strings.HasPrefix(prefix, "/") {
			c.errorf(ann.pos, "ServeAssets needs a path prefix starting with /")
			return
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil || types.ExprString(vs.Type) != "embed.FS" {
				continue
			}
			c.p.Assets = append(c.p.Assets, project.Assets{
				Prefix: prefix, Var: f.funcRef(vs.Names[0].Name, vs.Pos()),
			})
			return
		}
		c.errorf(ann.pos, "ServeAssets must be placed on a variable of type embed.FS")
	default:
		if isFeatureOrModifier(ann.Name) {
			c.errorf(ann.pos, "annotation %s must be placed on a function, not on a %s", ann.Name, strings.ToLower(gd.Tok.String()))
		} else {
			c.errorf(ann.pos, "unknown annotation %q", ann.Name)
		}
	}
}

// finish applies defaults and checks the references between features.
func (c *collector) finish() {
	if c.p.Server.Name == "" {
		parts := strings.Split(c.p.Module.Path, "/")
		c.p.Server.Name = parts[len(parts)-1]
	}
	if c.p.Server.Version == "" {
		c.p.Server.Version = "0.0.0"
	}
	if c.p.Transport.Endpoint == "" {
		c.p.Transport.Endpoint = "/mcp"
	}

	seen := map[string]project.Pos{}
	dup := func(kind, name string, pos project.Pos) {
		key := kind + " " + name
		if first, ok := seen[key]; ok {
			c.errorf(pos, "%s %q is already defined at %s", kind, name, first)
			return
		}
		seen[key] = pos
	}
	templates := map[string]bool{}
	prompts := map[string]bool{}
	for _, t := range c.p.Tools {
		dup("tool", t.Name, t.Func.Pos)
	}
	for _, r := range c.p.Resources {
		dup("resource", r.URI, r.Func.Pos)
		if r.Template {
			templates[r.URI] = true
		}
	}
	for _, pr := range c.p.Prompts {
		dup("prompt", pr.Name, pr.Func.Pos)
		prompts[pr.Name] = true
	}
	for _, l := range c.p.Lists {
		if !templates[l.Template] {
			c.errorf(l.Func.Pos, "ResourceList: no resource template %q", l.Template)
		}
	}
	for _, comp := range c.p.Completions {
		switch {
		case comp.Prompt != "" && !prompts[comp.Prompt]:
			c.errorf(comp.Pos, "Complete: unknown prompt %q", comp.Prompt)
		case comp.Template != "" && !templates[comp.Template]:
			c.errorf(comp.Pos, "Complete: no resource template %q", comp.Template)
		}
	}
}
