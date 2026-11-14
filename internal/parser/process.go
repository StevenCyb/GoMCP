package parser

import (
	"go/ast"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/annotation"
	"github.com/StevenCyb/GoMCP/internal/project"
)

// located is an annotation with its position.
type located struct {
	annotation.Annotation

	pos project.Pos
}

// annotated is the parsed content of a comment group.
type annotated struct {
	desc string
	anns []located
}

// parseGroup extracts the description and the annotations of a comment group.
func (c *collector) parseGroup(f *file, g *ast.CommentGroup) annotated {
	var out annotated
	if g == nil {
		return out
	}
	var desc []string
	for _, cm := range g.List {
		if strings.HasPrefix(cm.Text, "//go:") {
			continue // compiler directive
		}
		position := f.fset.Position(cm.Slash)
		lines, firstCol := commentLines(cm.Text, position.Column)
		for i, line := range lines {
			a, ok, err := annotation.ParseLine(line)
			pos := project.Pos{File: f.rel, Line: position.Line + i, Col: firstCol}
			switch {
			case err != nil:
				perr := err.(*annotation.Error) //nolint:errorlint,forcetypeassert // ParseLine only returns *annotation.Error
				pos.Col = firstCol + perr.Column - 1
				c.errorf(pos, "%s", perr.Msg)
			case ok:
				out.anns = append(out.anns, located{Annotation: a, pos: pos})
			default:
				if t := strings.TrimSpace(line); t != "" {
					desc = append(desc, t)
				}
			}
		}
	}
	out.desc = strings.Join(desc, " ")
	return out
}

// commentLines returns the text lines of a comment without the comment markers and the column
// where the text of the first line starts.
func commentLines(text string, col int) ([]string, int) {
	if rest, ok := strings.CutPrefix(text, "//"); ok {
		return []string{rest}, col + 2
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
	return strings.Split(body, "\n"), col + 2
}

// process walks all files and reads their annotations.
func (c *collector) process() {
	for _, f := range c.files {
		docs := map[*ast.CommentGroup]bool{}
		for _, decl := range f.ast.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				docs[d.Doc] = true
				c.funcDecl(f, d)
			case *ast.GenDecl:
				docs[d.Doc] = true
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						docs[s.Doc] = true
					case *ast.ValueSpec:
						docs[s.Doc] = true
					}
				}
				c.genDecl(f, d)
			}
		}
		for _, g := range f.ast.Comments {
			if docs[g] {
				continue
			}
			if a := c.parseGroup(f, g); len(a.anns) > 0 {
				c.packageLevel(f, a)
			}
		}
	}
}
