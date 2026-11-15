package parser

import (
	"go/ast"
	"time"

	"github.com/StevenCyb/GoMCP/internal/project"
)

func (c *collector) tool(f *file, feature located, a annotated, fd *ast.FuncDecl, ref project.FuncRef) {
	com := c.common(feature, a, ref, f)
	if com.Name == "" {
		c.errorf(feature.pos, "Tool needs a name")
		return
	}
	if !c.checkShape(fd, ref.Pos, "tool "+com.Name, 2, 2, "mcp.Context") {
		return
	}
	t := project.Tool{Common: com}
	for _, ann := range a.anns {
		switch ann.Name {
		case "Hint":
			c.hint(ann, &t.Hint)
		case "Task":
			c.task(ann, &t.Task)
		case "Requires":
			t.Requires = append(t.Requires, c.requires(ann)...)
		}
	}
	c.p.Tools = append(c.p.Tools, t)
}

func (c *collector) hint(ann located, h *project.Hint) {
	for _, flag := range ann.Positionals() {
		switch flag {
		case "readOnly":
			h.ReadOnly = true
		case "destructive":
			h.Destructive = true
		case "idempotent":
			h.Idempotent = true
		case "openWorld":
			h.OpenWorld = true
		default:
			c.errorf(ann.pos, "unknown hint %q, use readOnly, destructive, idempotent or openWorld", flag)
		}
	}
}

func (c *collector) task(ann located, t *project.Task) {
	for _, flag := range ann.Positionals() {
		switch flag {
		case "forbidden", "optional", "required":
			t.Mode = flag
		default:
			c.errorf(ann.pos, "unknown task mode %q, use forbidden, optional or required", flag)
		}
	}
	if t.Mode == "" {
		c.errorf(ann.pos, "Task needs a mode: forbidden, optional or required")
	}
	for _, key := range []string{"ttl", "poll"} {
		v, ok := ann.String(key)
		if !ok {
			continue
		}
		if _, err := time.ParseDuration(v); err != nil {
			c.errorf(ann.pos, "invalid duration %q for %s", v, key)
			continue
		}
		if key == "ttl" {
			t.TTL = v
		} else {
			t.Poll = v
		}
	}
}

func (c *collector) requires(ann located) []string {
	var out []string
	for _, flag := range ann.Positionals() {
		switch flag {
		case "sampling", "roots":
			out = append(out, flag)
		case "elicitation":
			if mode, ok := ann.String("elicitation"); ok && mode == "url" {
				continue
			}
			out = append(out, "elicitation")
		default:
			c.errorf(ann.pos, "unknown capability %q, use sampling, roots or elicitation", flag)
		}
	}
	if mode, ok := ann.String("elicitation"); ok {
		if mode != "url" {
			c.errorf(ann.pos, "elicitation=%q is not valid, use elicitation=\"url\"", mode)
		} else {
			out = append(out, "elicitation:url")
		}
	}
	return out
}
