package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

// CompleteFunc suggests values for a prompt argument or a resource template variable.
// It gets the partial value and the arguments resolved so far.
type CompleteFunc func(ctx Context, partial string, resolved map[string]string) (Completion, error)

const (
	kindResource = "resource"
	kindPrompt   = "prompt"
)

type completionKey struct {
	kind string // "prompt" or "resource"
	ref  string // prompt name or URI template
	arg  string
}

// Values returns a CompleteFunc that offers the values starting with the partial input.
func Values(values ...string) CompleteFunc {
	return func(_ Context, partial string, _ map[string]string) (Completion, error) {
		out := []string{}
		for _, v := range values {
			if strings.HasPrefix(v, partial) {
				out = append(out, v)
			}
		}
		return Completion{Values: out}, nil
	}
}

// AddPromptCompletion registers completion for an argument of a prompt.
func (s *Server) AddPromptCompletion(prompt, arg string, fn CompleteFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.promptIdx[prompt]; !ok {
		return fmt.Errorf("%w: prompt %q", errCompletionTarget, prompt)
	}
	s.completions[completionKey{kind: kindPrompt, ref: prompt, arg: arg}] = fn
	return nil
}

// AddResourceCompletion registers completion for a variable of a resource template.
func (s *Server) AddResourceCompletion(template, arg string, fn CompleteFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.templates {
		if t.def.URI == template {
			s.completions[completionKey{kind: kindResource, ref: template, arg: arg}] = fn
			return nil
		}
	}
	return fmt.Errorf("%w: resource template %q", errCompletionTarget, template)
}

const maxCompletionValues = 100

func (s *Server) complete(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	var p struct {
		Ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
		Context struct {
			Arguments map[string]string `json:"arguments"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, rpc.InvalidParams("invalid completion request: " + err.Error())
	}
	var key completionKey
	switch p.Ref.Type {
	case "ref/prompt":
		key = completionKey{kind: kindPrompt, ref: p.Ref.Name, arg: p.Argument.Name}
	case "ref/resource":
		key = completionKey{kind: kindResource, ref: p.Ref.URI, arg: p.Argument.Name}
	default:
		return nil, rpc.InvalidParams("unknown completion ref type " + p.Ref.Type)
	}

	s.mu.RLock()
	fn := s.completions[key]
	s.mu.RUnlock()
	if fn == nil {
		return map[string]any{"completion": Completion{Values: []string{}}}, nil
	}

	core := func(c Context, _ Request) (any, error) {
		return fn(c, p.Argument.Value, p.Context.Arguments)
	}
	out, err := s.chainFor(core, nil)(ctx, Request{Kind: KindCompletion, Name: key.ref, Params: params})
	if err != nil {
		return nil, toRPCError(err)
	}
	c, _ := out.(Completion)
	if c.Values == nil {
		c.Values = []string{}
	}
	if len(c.Values) > maxCompletionValues {
		c.Total = max(c.Total, len(c.Values))
		c.Values = c.Values[:maxCompletionValues]
		c.HasMore = true
	}
	return map[string]any{"completion": c}, nil
}
