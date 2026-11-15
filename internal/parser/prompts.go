package parser

import (
	"go/ast"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

// completionFunc handles functions with only Complete annotations: dynamic completion.
func (c *collector) completionFunc(f *file, fd *ast.FuncDecl, a annotated) {
	var completes []located
	for _, ann := range a.anns {
		if ann.Name != "Complete" {
			switch {
			case ann.Name == "ServeAssets":
				c.errorf(ann.pos, "ServeAssets must be placed on a variable of type embed.FS")
			case ann.Name == "Elicitation":
				c.errorf(ann.pos, "Elicitation must be placed on a struct type")
			case isFeatureOrModifier(ann.Name):
				c.errorf(ann.pos, "annotation %s needs a feature annotation (Tool, Resource, Prompt, ...) on the same function", ann.Name)
			default:
				c.errorf(ann.pos, "unknown annotation %q", ann.Name)
			}
			return
		}
		completes = append(completes, ann)
	}
	pos := f.pos(fd.Pos())
	if !c.checkShape(fd, pos, "completion", 3, 2, "") {
		return
	}
	ref := f.funcRef(fd.Name.Name, fd.Pos())
	for _, ann := range completes {
		target, hasFor := ann.String("for")
		arg, hasArg := ann.String("arg")
		if !hasFor || !hasArg {
			c.errorf(ann.pos, "Complete on a completion function needs for=\"...\" and arg=\"...\"")
			continue
		}
		comp := project.Completion{Arg: arg, Func: &ref, Pos: ann.pos}
		if strings.Contains(target, "{") || strings.Contains(target, "://") {
			comp.Template = target
		} else {
			comp.Prompt = target
		}
		c.p.Completions = append(c.p.Completions, comp)
	}
}

func (c *collector) prompt(f *file, feature located, a annotated, fd *ast.FuncDecl, ref project.FuncRef) {
	com := c.common(feature, a, ref, f)
	if com.Name == "" {
		c.errorf(feature.pos, "Prompt needs a name")
		return
	}
	if !c.checkShape(fd, ref.Pos, "prompt "+com.Name, 2, 2, "mcp.Context") {
		return
	}
	c.p.Prompts = append(c.p.Prompts, project.Prompt{Common: com})
	for _, ann := range a.anns {
		if ann.Name != "Complete" {
			continue
		}
		arg, hasArg := ann.String("arg")
		values, hasValues := ann.String("values")
		if !hasArg || !hasValues {
			c.errorf(ann.pos, "Complete on a prompt needs arg=\"...\" and values=\"a,b\"; use a completion function with for= for dynamic values")
			continue
		}
		c.p.Completions = append(c.p.Completions, project.Completion{
			Prompt: com.Name, Arg: arg, Values: strings.Split(values, ","), Pos: ann.pos,
		})
	}
}
