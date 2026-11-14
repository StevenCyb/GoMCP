package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/schema"
)

// PromptDef describes a prompt.
type PromptDef struct {
	Name        string
	Title       string
	Description string
	Icons       []Icon
	Meta        map[string]any
	Scopes      []string
	Use         []Middleware
}

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type prompt struct {
	def  PromptDef
	args []promptArg
	get  func(Context, map[string]string) (Messages, error)
}

// AddPrompt registers a prompt. A is a struct whose fields are the prompt arguments;
// a field is required unless it has omitempty.
func AddPrompt[A any](s *Server, def PromptDef, h func(Context, A) (Messages, error)) error {
	if def.Name == "" {
		return errNameRequired
	}
	sch, isObject, err := schema.ObjectFor(reflect.TypeFor[A]())
	if err != nil {
		return fmt.Errorf("prompt %q arguments: %w", def.Name, err)
	}
	if !isObject {
		return fmt.Errorf("prompt %q: arguments must be a struct", def.Name)
	}
	p := &prompt{def: def, args: promptArgs(sch)}
	p.get = func(ctx Context, args map[string]string) (Messages, error) {
		for _, a := range p.args {
			if _, ok := args[a.Name]; a.Required && !ok {
				return nil, ErrInvalidParams("missing argument " + a.Name)
			}
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("encode arguments: %w", err)
		}
		var in A
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, ErrInvalidParams("invalid arguments: " + err.Error())
		}
		return h(ctx, in)
	}
	return s.addPrompt(p)
}

func promptArgs(sch map[string]any) []promptArg {
	props, _ := sch["properties"].(map[string]any)
	required, _ := sch["required"].([]string)
	names := slices.Sorted(maps.Keys(props))
	out := make([]promptArg, 0, len(names))
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		desc, _ := prop["description"].(string)
		a := promptArg{Name: name, Description: desc}
		a.Required = slices.Contains(required, name)
		out = append(out, a)
	}
	return out
}

func (s *Server) addPrompt(p *prompt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.promptIdx[p.def.Name]; ok {
		return duplicate("prompt", p.def.Name)
	}
	s.promptIdx[p.def.Name] = p
	s.prompts = append(s.prompts, p)
	return nil
}

var errCompletionTarget = errors.New("mcp: completion target is not registered")

type promptEntry struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Arguments   []promptArg    `json:"arguments,omitempty"`
	Icons       []Icon         `json:"icons,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"` //nolint:tagliatelle // field name defined by the MCP specification
}

func (s *Server) listPrompts(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	req, rpcErr := s.pageRequest(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.RLock()
	entries := make([]promptEntry, 0, len(s.prompts))
	for _, p := range s.prompts {
		entries = append(entries, promptEntry{
			Name: p.def.Name, Title: p.def.Title, Description: p.def.Description,
			Arguments: p.args, Icons: s.resolveIcons(p.def.Icons, ctx.call.HTTPRequest()), Meta: p.def.Meta,
		})
	}
	s.mu.RUnlock()
	page := Paginate(entries, req)
	return listResult("prompts", page.Items, page.Next), nil
}

func (s *Server) getPrompt(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, rpc.InvalidParams("invalid prompt request: " + err.Error())
	}
	s.mu.RLock()
	pr := s.promptIdx[p.Name]
	s.mu.RUnlock()
	if pr == nil {
		return nil, rpc.InvalidParams("unknown prompt " + p.Name)
	}

	if rpcErr := ctx.loadInput("prompts/get", params); rpcErr != nil {
		return nil, rpcErr
	}
	core := func(c Context, _ Request) (any, error) {
		msgs, err := pr.get(c, p.Arguments)
		if err != nil {
			return nil, err
		}
		return s.resolveEmbeds(c, msgs)
	}
	out, err := s.chainFor(core, pr.def.Use)(ctx, Request{Kind: KindPrompt, Name: p.Name, Params: params})
	if len(ctx.asked) > 0 {
		return ctx.inputRequired(), nil
	}
	if err != nil {
		return nil, toRPCError(err)
	}
	msgs, _ := out.(Messages)
	if msgs == nil {
		msgs = Messages{}
	}
	res := map[string]any{"messages": msgs}
	if pr.def.Description != "" {
		res["description"] = pr.def.Description
	}
	return res, nil
}

// resolveEmbeds replaces EmbedResource placeholders by the contents of the resource.
func (s *Server) resolveEmbeds(ctx Context, msgs Messages) (Messages, error) {
	out := make(Messages, len(msgs))
	copy(out, msgs)
	for i := range out {
		uri := out[i].Content.embed
		if uri == "" {
			continue
		}
		contents, err := s.readResource(ctx, uri)
		if err != nil {
			return nil, err
		}
		if len(contents) == 0 {
			return nil, notFound(uri)
		}
		out[i].Content = Content{Type: typeResource, Resource: &contents[0]}
	}
	return out, nil
}
