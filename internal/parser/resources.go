package parser

import (
	"go/ast"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

func (c *collector) resource(f *file, feature located, a annotated, fd *ast.FuncDecl, ref project.FuncRef) {
	uri, ok := feature.Positional()
	if !ok {
		c.errorf(feature.pos, "Resource needs a URI or URI template")
		return
	}
	com := c.common(feature, a, ref, f)
	com.Name = fd.Name.Name
	r := project.Resource{Common: com, URI: uri, Template: strings.Contains(uri, "{")}
	r.MIME, _ = feature.String("mime")
	if r.Template {
		if !c.checkShape(fd, ref.Pos, "resource template "+uri, 2, 2, "mcp.Context") {
			return
		}
	} else if !c.checkShape(fd, ref.Pos, "static resource "+uri, 1, 2, "mcp.Context") {
		return
	}
	for _, ann := range a.anns {
		switch ann.Name {
		case "Subscribable":
			r.Subscribable = true
		case "Annotations":
			r.Annotations = c.annotations(ann)
		}
	}
	c.p.Resources = append(c.p.Resources, r)
}

func (c *collector) annotations(ann located) *project.Annotations {
	out := &project.Annotations{}
	if audience, ok := ann.String("audience"); ok {
		out.Audience = strings.Split(audience, ",")
	}
	if prio, ok := ann.Number("priority"); ok {
		out.Priority = &prio
	}
	out.LastModified, _ = ann.String("lastModified")
	return out
}

func (c *collector) resourceList(feature located, fd *ast.FuncDecl, ref project.FuncRef) {
	tmpl, ok := feature.Positional()
	if !ok {
		c.errorf(feature.pos, "ResourceList needs the template of the resources it lists")
		return
	}
	if c.checkShape(fd, ref.Pos, "resource list", 2, 2, "mcp.Context") {
		c.p.Lists = append(c.p.Lists, project.ResourceList{Template: tmpl, Func: ref})
	}
}
