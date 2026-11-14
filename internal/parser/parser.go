// Package parser reads the @MCP.* annotations of a Go module into a project model.
package parser

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

// Diagnostic is one problem found in the source.
type Diagnostic struct {
	Pos project.Pos
	Msg string
}

// Errors is the list of problems found; Parse reports all of them at once.
type Errors []Diagnostic

func (e Errors) Error() string {
	lines := make([]string, 0, len(e))
	for _, d := range e {
		lines = append(lines, d.Pos.String()+": "+d.Msg)
	}
	return strings.Join(lines, "\n")
}

// Parse reads the module that contains dir.
func Parse(dir string) (*project.Project, error) {
	root, modPath, err := FindModule(dir)
	if err != nil {
		return nil, err
	}
	c := &collector{
		p:       &project.Project{Module: project.Module{Path: modPath, Dir: root}},
		funcs:   map[string]map[string]*funcInfo{},
		pkgName: map[string]string{},
		byName:  map[string][]string{},
	}
	if err := c.load(root, modPath); err != nil {
		return nil, err
	}
	c.process()
	c.finish()
	if len(c.diags) > 0 {
		sort.SliceStable(c.diags, func(i, j int) bool {
			a, b := c.diags[i].Pos, c.diags[j].Pos
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
		return nil, c.diags
	}
	return c.p, nil
}

// FindModule looks for go.mod in dir and its parents and returns the module root and path.
func FindModule(dir string) (string, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		data, err := os.ReadFile(filepath.Join(cur, "go.mod")) //nolint:gosec // the path is the module directory chosen by the user
		if err == nil {
			modPath := modulePath(string(data))
			if modPath == "" {
				return "", "", fmt.Errorf("%s: go.mod has no module line", filepath.Join(cur, "go.mod"))
			}
			return cur, modPath, nil
		}
		if filepath.Dir(cur) == cur {
			return "", "", fmt.Errorf("no go.mod found in or above %s", abs)
		}
	}
}

func modulePath(goMod string) string {
	for line := range strings.SplitSeq(goMod, "\n") {
		line, _, _ = strings.Cut(line, "//")
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"`+"`")
	}
	return ""
}

// file is a parsed source file.
type file struct {
	rel        string // path relative to the module directory
	importPath string
	pkg        string
	fset       *token.FileSet
	ast        *ast.File
}

// funcInfo is a function declaration of the user's code.
type funcInfo struct {
	decl *ast.FuncDecl
	ref  project.FuncRef
}

type collector struct {
	p     *project.Project
	diags Errors

	serverSeen bool
	files      []*file
	funcs      map[string]map[string]*funcInfo // import path -> name -> function
	pkgName    map[string]string               // import path -> package name
	byName     map[string][]string             // package name -> import paths
}

func (c *collector) errorf(pos project.Pos, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// SkipDir reports whether a directory is not part of the module sources.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata"
}

// load parses all source files of the module.
func (c *collector) load(root, modPath string) error {
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(p, "go.mod")); p != root && statErr == nil {
				return filepath.SkipDir // nested module
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		return c.loadFile(root, modPath, p)
	})
	if err != nil {
		return fmt.Errorf("read module: %w", err)
	}
	return nil
}

func (c *collector) loadFile(root, modPath, p string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return fmt.Errorf("relative path of %s: %w", p, err)
	}
	rel = filepath.ToSlash(rel)
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, p, nil, goparser.ParseComments)
	if err != nil {
		c.errorf(project.Pos{File: rel, Line: 1}, "%v", err)
		return nil
	}
	if f.Name.Name == "main" {
		return nil
	}
	importPath := modPath
	if dir := path.Dir(rel); dir != "." {
		importPath = modPath + "/" + dir
	}
	fl := &file{rel: rel, importPath: importPath, pkg: f.Name.Name, fset: fset, ast: f}
	c.files = append(c.files, fl)

	if _, ok := c.pkgName[importPath]; !ok {
		c.pkgName[importPath] = fl.pkg
		c.byName[fl.pkg] = append(c.byName[fl.pkg], importPath)
		c.funcs[importPath] = map[string]*funcInfo{}
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil {
			c.funcs[importPath][fd.Name.Name] = &funcInfo{decl: fd, ref: fl.funcRef(fd.Name.Name, fd.Pos())}
		}
	}
	return nil
}

func (f *file) pos(p token.Pos) project.Pos {
	position := f.fset.Position(p)
	return project.Pos{File: f.rel, Line: position.Line, Col: position.Column}
}

func (f *file) funcRef(name string, p token.Pos) project.FuncRef {
	return project.FuncRef{Pkg: f.pkg, ImportPath: f.importPath, Name: name, Pos: f.pos(p)}
}
