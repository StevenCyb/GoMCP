package mcp

import (
	"encoding/json"
	"slices"
)

// Kind is the type of feature a middleware wraps.
type Kind string

// Kinds of features.
const (
	KindTool       Kind = "tool"
	KindResource   Kind = "resource"
	KindPrompt     Kind = "prompt"
	KindCompletion Kind = "completion"
)

// Request describes a feature call for middleware.
type Request struct {
	Kind   Kind
	Name   string // tool or prompt name, resource URI
	Params json.RawMessage
}

// Handler is the next step of a middleware chain.
type Handler func(Context, Request) (any, error)

// Middleware wraps tool calls, resource reads, prompt gets and completions.
type Middleware func(ctx Context, req Request, next Handler) (any, error)

// chain wraps h with the middleware, the first one being the outermost.
func chain(h Handler, mws ...Middleware) Handler {
	for _, v := range slices.Backward(mws) {
		mw, next := v, h
		h = func(ctx Context, req Request) (any, error) { return mw(ctx, req, next) }
	}
	return h
}
