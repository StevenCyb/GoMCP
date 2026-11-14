package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/schema"
)

// ResourceDef describes a resource or a resource template.
type ResourceDef struct {
	// URI is a fixed URI, or an RFC 6570 style template such as note:///{id}.
	URI          string
	Name         string
	Title        string
	Description  string
	MIMEType     string
	Icons        []Icon
	Annotations  *Annotations
	Subscribable bool
	Meta         map[string]any
	Scopes       []string
	Use          []Middleware
}

type resource struct {
	def  ResourceDef
	tmpl *uriTemplate
	read func(Context, string, map[string]string) (Contents, error)
}

type resourceLister struct {
	template string
	mime     string
	list     func(Context, PageReq) (Page[ResourceRef], error)
}

// uriTemplate matches URIs against a template with {variables}.
type uriTemplate struct {
	re   *regexp.Regexp
	vars []string
}

var templateVar = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func parseTemplate(uri string) *uriTemplate {
	var sb strings.Builder
	matches := templateVar.FindAllStringSubmatchIndex(uri, -1)
	vars := make([]string, 0, len(matches))
	last := 0
	sb.WriteString("^")
	for _, m := range matches {
		sb.WriteString(regexp.QuoteMeta(uri[last:m[0]]))
		sb.WriteString(`([^/?#]+)`)
		vars = append(vars, uri[m[2]:m[3]])
		last = m[1]
	}
	sb.WriteString(regexp.QuoteMeta(uri[last:]))
	sb.WriteString("$")
	return &uriTemplate{re: regexp.MustCompile(sb.String()), vars: vars}
}

func (t *uriTemplate) match(uri string) (map[string]string, bool) {
	m := t.re.FindStringSubmatch(uri)
	if m == nil {
		return nil, false
	}
	out := make(map[string]string, len(t.vars))
	for i, name := range t.vars {
		out[name] = m[i+1]
	}
	return out, true
}

func isTemplate(uri string) bool { return templateVar.MatchString(uri) }

// AddResource registers a resource template. P is a struct whose uri tagged fields receive the
// template variables.
func AddResource[P any](s *Server, def ResourceDef, h func(Context, P) (Contents, error)) error {
	if def.URI == "" {
		return errors.New("mcp: resource URI is required")
	}
	if !isTemplate(def.URI) {
		return fmt.Errorf("mcp: resource %q is not a template, use AddStaticResource", def.URI)
	}
	tmpl := parseTemplate(def.URI)
	for _, v := range schema.URIVars(reflect.TypeFor[P]()) {
		if !slices.Contains(tmpl.vars, v) {
			return fmt.Errorf("mcp: resource %q: uri tag %q is not a variable of the template", def.URI, v)
		}
	}
	r := &resource{def: def, tmpl: tmpl}
	r.read = func(ctx Context, _ string, vars map[string]string) (Contents, error) {
		var p P
		if err := schema.BindURI(vars, &p); err != nil {
			return nil, ErrInvalidParams(err.Error())
		}
		return h(ctx, p)
	}
	return s.addResource(r)
}

// AddStaticResource registers a resource with a fixed URI.
func AddStaticResource(s *Server, def ResourceDef, h func(Context) (Contents, error)) error {
	if def.URI == "" {
		return errors.New("mcp: resource URI is required")
	}
	if isTemplate(def.URI) {
		return fmt.Errorf("mcp: resource %q is a template, use AddResource", def.URI)
	}
	r := &resource{def: def, read: func(ctx Context, _ string, _ map[string]string) (Contents, error) { return h(ctx) }}
	return s.addResource(r)
}

func (s *Server) addResource(r *resource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.tmpl == nil {
		if _, ok := s.staticIdx[r.def.URI]; ok {
			return duplicate("resource", r.def.URI)
		}
		s.staticIdx[r.def.URI] = r
		s.statics = append(s.statics, r)
		return nil
	}
	for _, t := range s.templates {
		if t.def.URI == r.def.URI {
			return duplicate("resource template", r.def.URI)
		}
	}
	s.templates = append(s.templates, r)
	return nil
}

// AddResourceLister registers the function that lists the concrete resources of a template.
func (s *Server) AddResourceLister(template string, fn func(Context, PageReq) (Page[ResourceRef], error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.templates {
		if t.def.URI == template {
			s.listers = append(s.listers, resourceLister{template: template, mime: t.def.MIMEType, list: fn})
			return nil
		}
	}
	return fmt.Errorf("mcp: no resource template %q registered", template)
}

type resourceEntry struct {
	URI         string         `json:"uri,omitempty"`
	URITemplate string         `json:"uriTemplate,omitempty"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MIMEType    string         `json:"mimeType,omitempty"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Icons       []Icon         `json:"icons,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"` //nolint:tagliatelle // field name defined by the MCP specification
}

func (s *Server) entryOf(r *resource, req *http.Request) resourceEntry {
	e := resourceEntry{
		Name: r.def.Name, Title: r.def.Title, Description: r.def.Description, MIMEType: r.def.MIMEType,
		Annotations: r.def.Annotations, Icons: s.resolveIcons(r.def.Icons, req), Meta: r.def.Meta,
	}
	if r.tmpl != nil {
		e.URITemplate = r.def.URI
	} else {
		e.URI = r.def.URI
	}
	return e
}

func (s *Server) listResources(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	req, rpcErr := s.pageRequest(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.RLock()
	entries := make([]resourceEntry, 0, len(s.statics))
	for _, r := range s.statics {
		entries = append(entries, s.entryOf(r, ctx.call.HTTPRequest()))
	}
	listers := append([]resourceLister(nil), s.listers...)
	s.mu.RUnlock()

	for _, l := range listers {
		page, err := l.list(ctx, PageReq{})
		if err != nil {
			return nil, toRPCError(err)
		}
		for _, ref := range page.Items {
			if ref.MIMEType == "" {
				ref.MIMEType = l.mime
			}
			entries = append(entries, resourceEntry{
				URI: ref.URI, Name: ref.Name, Title: ref.Title, Description: ref.Description, MIMEType: ref.MIMEType,
			})
		}
	}
	page := Paginate(entries, req)
	return listResult("resources", page.Items, page.Next), nil
}

func (s *Server) listTemplates(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	req, rpcErr := s.pageRequest(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.RLock()
	entries := make([]resourceEntry, 0, len(s.templates))
	for _, r := range s.templates {
		entries = append(entries, s.entryOf(r, ctx.call.HTTPRequest()))
	}
	s.mu.RUnlock()
	page := Paginate(entries, req)
	return listResult("resourceTemplates", page.Items, page.Next), nil
}

// findResource returns the resource serving uri; an exact URI wins over templates.
func (s *Server) findResource(uri string) (*resource, map[string]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r, ok := s.staticIdx[uri]; ok {
		return r, nil
	}
	for _, r := range s.templates {
		if vars, ok := r.tmpl.match(uri); ok {
			return r, vars
		}
	}
	return nil, nil
}

func notFound(uri string) *rpc.Error {
	return &rpc.Error{Code: rpc.CodeInvalidParams, Message: "resource not found: " + uri, Data: map[string]string{"uri": uri}}
}

// readResource reads a resource and fills in the URI and MIME type the handler left out.
func (s *Server) readResource(ctx Context, uri string) (Contents, error) {
	r, vars := s.findResource(uri)
	if r == nil {
		return nil, notFound(uri)
	}
	contents, err := r.read(ctx, uri, vars)
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(uri) // the handler does not know the uri, the error should
	}
	if err != nil {
		return nil, err
	}
	for i := range contents {
		if contents[i].URI == "" {
			contents[i].URI = uri
		}
		if contents[i].MIMEType == "" {
			contents[i].MIMEType = r.def.MIMEType
		}
	}
	return contents, nil
}

func (s *Server) readResourceCall(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return nil, rpc.InvalidParams("resources/read needs a uri")
	}
	if rpcErr := ctx.loadInput("resources/read", params); rpcErr != nil {
		return nil, rpcErr
	}
	var use []Middleware
	if r, _ := s.findResource(p.URI); r != nil {
		use = r.def.Use
	}
	core := func(c Context, req Request) (any, error) { return s.readResource(c, req.Name) }
	out, err := s.chainFor(core, use)(ctx, Request{Kind: KindResource, Name: p.URI, Params: params})
	if len(ctx.asked) > 0 {
		return ctx.inputRequired(), nil
	}
	if err != nil {
		return nil, toRPCError(err)
	}
	contents, _ := out.(Contents)
	if contents == nil {
		contents = Contents{}
	}
	return map[string]any{"contents": contents}, nil
}
