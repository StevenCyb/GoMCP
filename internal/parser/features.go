package parser

import (
	"go/ast"
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/project"
)

// featureModifiers maps each feature annotation to the modifier annotations it accepts.
var featureModifiers = map[string]map[string]bool{ //nolint:gochecknoglobals // constant lookup table
	"Tool":         set("Hint", "Task", "Requires", "AuthScopes", "Icon", "Meta", "Use"),
	"Resource":     set("Subscribable", "Annotations", "AuthScopes", "Icon", "Meta", "Use"),
	"Prompt":       set("Complete", "AuthScopes", "Icon", "Meta", "Use"),
	"ResourceList": set(),
	"Middleware":   set(),
	"Setup":        set(),
	"Teardown":     set(),
	"HTTPHandler":  set(),
	"Auth":         set("AuthServer", "AuthScope"),
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// repeatable lists the modifiers that may appear more than once.
var repeatable = set("Requires", "AuthScopes", "Icon", "Meta", "Use", "Complete", "AuthServer", "AuthScope") //nolint:gochecknoglobals // constant lookup table

func (c *collector) funcDecl(f *file, fd *ast.FuncDecl) {
	a := c.parseGroup(f, fd.Doc)
	if len(a.anns) == 0 {
		return
	}
	pos := f.pos(fd.Pos())

	// the feature annotation decides what the function is
	var features []located
	for _, ann := range a.anns {
		if _, ok := featureModifiers[ann.Name]; ok {
			features = append(features, ann)
		}
	}
	switch {
	case len(features) > 1:
		names := make([]string, 0, len(features))
		for _, ft := range features {
			names = append(names, ft.Name)
		}
		c.errorf(features[1].pos, "a function can only be one feature, found more than one: %s", strings.Join(names, ", "))
		return
	case len(features) == 0:
		c.completionFunc(f, fd, a)
		return
	}

	feature := features[0]
	if !c.checkModifiers(feature, a) {
		return
	}
	if fd.Recv != nil {
		c.errorf(pos, "%s must be a plain function, not a method (it has a receiver)", feature.Name)
		return
	}
	ref := f.funcRef(fd.Name.Name, fd.Pos())

	switch feature.Name {
	case "Tool":
		c.tool(f, feature, a, fd, ref)
	case "Resource":
		c.resource(f, feature, a, fd, ref)
	case "ResourceList":
		c.resourceList(feature, fd, ref)
	case "Prompt":
		c.prompt(f, feature, a, fd, ref)
	case "Middleware":
		c.checkShape(fd, pos, "middleware", 3, 2, "")
	case "Setup":
		c.setup(fd, pos, ref)
	case "Teardown":
		if c.checkShape(fd, pos, "Teardown", 0, 0, "") {
			c.p.Teardown = append(c.p.Teardown, ref)
		}
	case "Auth":
		c.auth(feature, a, fd, ref)
	case "HTTPHandler":
		c.httpHandler(feature, fd, ref)
	}
}

// checkModifiers rejects modifiers that do not belong to the feature and duplicates.
func (c *collector) checkModifiers(feature located, a annotated) bool {
	ok := true
	valid := featureModifiers[feature.Name]
	seen := map[string]bool{}
	for _, ann := range a.anns {
		if ann.Name == feature.Name {
			continue
		}
		if _, isFeature := featureModifiers[ann.Name]; isFeature {
			continue
		}
		switch {
		case !valid[ann.Name]:
			if isFeatureOrModifier(ann.Name) {
				c.errorf(ann.pos, "annotation %s is not valid on a %s", ann.Name, feature.Name)
			} else {
				c.errorf(ann.pos, "unknown annotation %q", ann.Name)
			}
			ok = false
		case seen[ann.Name] && !repeatable[ann.Name]:
			c.errorf(ann.pos, "annotation %s appears more than once", ann.Name)
			ok = false
		}
		seen[ann.Name] = true
	}
	return ok
}

// common reads what every feature shares; name is empty if the feature has none yet.
func (c *collector) common(feature located, a annotated, ref project.FuncRef, f *file) project.Common {
	com := project.Common{Func: ref, Description: a.desc}
	if name, ok := feature.Positional(); ok {
		com.Name = name
	}
	if title, ok := feature.String("title"); ok {
		com.Title = title
	}
	if desc, ok := feature.String("description"); ok {
		com.Description = desc
	}
	meta := map[string]any{}
	for _, ann := range a.anns {
		switch ann.Name {
		case "Icon":
			if icon, ok := c.icon(ann); ok {
				com.Icons = append(com.Icons, icon)
			}
		case "AuthScopes":
			com.Scopes = append(com.Scopes, ann.Positionals()...)
		case "Use":
			if use, ok := c.useRef(f, ann); ok {
				com.Use = append(com.Use, use)
			}
		case "Meta":
			c.meta(ann, meta)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(meta)) {
		com.Meta = append(com.Meta, project.KV{Key: k, Value: meta[k]})
	}
	return com
}

func (c *collector) meta(ann located, into map[string]any) {
	for _, arg := range ann.Args {
		if arg.Key == "" {
			c.errorf(ann.pos, "Meta takes key=value arguments")
			continue
		}
		switch {
		case arg.Value.Kind == 1: // number
			into[arg.Key] = arg.Value.Num
		case arg.Value.Kind == 2 && (arg.Value.Str == "true" || arg.Value.Str == "false"): // identifier
			into[arg.Key] = arg.Value.Str == "true"
		default:
			into[arg.Key] = arg.Value.Str
		}
	}
}

// checkShape validates the signature of a handler: parameter and result counts, the
// first parameter type and (for 2 results) that the last one is an error.
func (c *collector) checkShape(fd *ast.FuncDecl, pos project.Pos, what string, params, results int, firstParam string) bool {
	if got := fd.Type.Params.NumFields(); got != params {
		switch params {
		case 0:
			c.errorf(pos, "%s must take no parameters, has %d", what, got)
		case 1:
			c.errorf(pos, "%s takes 1 parameter (ctx), has %d", what, got)
		default:
			c.errorf(pos, "%s must have %d parameters, has %d", what, params, got)
		}
		return false
	}
	if got := fd.Type.Results.NumFields(); got != results {
		c.errorf(pos, "%s must return %d results (value and error), returns %d", what, results, got)
		return false
	}
	if results == 2 {
		last := fd.Type.Results.List[len(fd.Type.Results.List)-1].Type
		if types.ExprString(last) != "error" {
			c.errorf(pos, "the last result of %s must be error", what)
			return false
		}
	}
	if firstParam != "" {
		first := types.ExprString(fd.Type.Params.List[0].Type)
		if !strings.HasSuffix(firstParam, ".Context") || !strings.HasSuffix(first, ".Context") {
			c.errorf(pos, "the first parameter of %s must be %s, is %s", what, firstParam, first)
			return false
		}
	}
	return true
}

// useRef resolves a Use annotation to a middleware function.
func (c *collector) useRef(f *file, ann located) (project.FuncRef, bool) {
	ident, ok := ann.Positional()
	if !ok {
		c.errorf(ann.pos, "Use needs the name of a middleware function")
		return project.FuncRef{}, false
	}
	pkgPath := f.importPath
	name := ident
	if pkgName, fn, dotted := strings.Cut(ident, "."); dotted {
		name = fn
		if f.pkg == pkgName {
			pkgPath = f.importPath
		} else {
			paths := c.byName[pkgName]
			switch len(paths) {
			case 0:
				c.errorf(ann.pos, "unknown package %q in %s", pkgName, ident)
				return project.FuncRef{}, false
			case 1:
				pkgPath = paths[0]
			default:
				c.errorf(ann.pos, "package name %q is ambiguous: %s", pkgName, strings.Join(paths, ", "))
				return project.FuncRef{}, false
			}
		}
	}
	info := c.funcs[pkgPath][name]
	if info == nil {
		c.errorf(ann.pos, "unknown function %q", ident)
		return project.FuncRef{}, false
	}
	if !c.checkShape(info.decl, ann.pos, "middleware "+ident, 3, 2, "") {
		return project.FuncRef{}, false
	}
	return info.ref, true
}
