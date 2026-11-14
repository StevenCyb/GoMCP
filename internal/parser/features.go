package parser

import (
	"go/ast"
	"go/types"
	"maps"
	"slices"
	"strings"
	"time"

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
