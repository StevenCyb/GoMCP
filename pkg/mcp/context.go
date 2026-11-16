package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/schema"
	"github.com/StevenCyb/GoMCP/internal/transport"
)

// Context is passed to every handler. It carries the request scope (cancellation when the
// client closes the connection) and the calls that need the client.
//
// Sample, Roots, Elicit and ElicitURL work through multi round-trip requests: when the client has
// not answered yet they return ErrInputRequired, the handler returns it as is, the client answers
// and the server runs the handler again from the top. Do the calls that need the client first.
type Context interface {
	context.Context

	// Log sends log notifications on the response stream, if the request asked for them with a log level.
	Log() Logger
	// Progress reports progress if the client asked for it with a progress token.
	Progress(progress, total float64, message string)
	// Principal returns the authenticated caller; it is empty without authentication.
	Principal() Principal

	// Sample asks the client's LLM for a completion (deprecated by the specification).
	Sample(req SampleReq) (SampleRes, error)
	// Roots returns the client's filesystem roots (deprecated by the specification).
	Roots() ([]Root, error)
	// ElicitURL sends the user to a web page; {id} in url is replaced by the elicitation id.
	// It returns once the user accepted; the page's backend ends the wait with CompleteElicitation.
	ElicitURL(message, url string) (string, error)
	// ElicitDone is closed when the URL elicitation with that id completed. For an unknown, expired or
	// foreign id it never closes: also select on Done() and a timeout.
	ElicitDone(id string) <-chan struct{}

	// ResourceUpdated tells the listeners subscribed to uri that its content changed.
	ResourceUpdated(uri string)
	// ResourcesChanged, ToolsChanged and PromptsChanged tell the listeners that a list changed.
	ResourcesChanged()
	ToolsChanged()
	PromptsChanged()

	elicitForm(message string, requested map[string]any) (Action, json.RawMessage, error)
}

// Logger sends log notifications to the client.
type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

// inputRequest is a question to the client that is part of an InputRequiredResult.
type inputRequest struct {
	key    string
	method string
	params any
}

// callContext implements Context for one request, or for a task running in the background.
type callContext struct {
	context.Context //nolint:containedctx // Context embeds context.Context by design

	srv       *Server
	call      *transport.Call // nil inside a task
	meta      transport.Meta
	caps      clientCaps
	principal Principal
	trusted   bool // the caller needs no scopes: calls over stdio come from the process that started the server

	// multi round-trip state of this request
	responses map[string]json.RawMessage
	state     map[string]string // data of the verified requestState
	digest    string
	counters  map[string]int
	asked     []inputRequest
	stateOut  map[string]string

	task *taskEntry // set while the handler runs as a task
}

func (s *Server) newContext(call *transport.Call) *callContext {
	meta := call.Meta()
	var caps clientCaps
	_ = json.Unmarshal(meta.Capabilities, &caps)
	p, _ := call.Principal().(Principal)
	return &callContext{
		Context: call.Context(), srv: s, call: call, meta: meta, caps: caps, principal: p, trusted: call.Trusted(),
		responses: map[string]json.RawMessage{}, state: map[string]string{}, counters: map[string]int{}, stateOut: map[string]string{},
	}
}

func (c *callContext) Principal() Principal { return c.principal }

func (c *callContext) notify(method string, params any) {
	if c.call != nil {
		c.call.Notify(method, params)
	}
}

func (c *callContext) Progress(progress, total float64, message string) {
	if len(c.meta.ProgressToken) == 0 || c.task != nil {
		return
	}
	params := map[string]any{"progressToken": c.meta.ProgressToken, "progress": progress}
	if total > 0 {
		params["total"] = total
	}
	if message != "" {
		params["message"] = message
	}
	c.notify("notifications/progress", params)
}

func (c *callContext) ResourceUpdated(uri string) { c.srv.ResourceUpdated(uri) }
func (c *callContext) ResourcesChanged()          { c.srv.ResourcesChanged() }
func (c *callContext) ToolsChanged()              { c.srv.ToolsChanged() }
func (c *callContext) PromptsChanged()            { c.srv.PromptsChanged() }
func (c *callContext) Log() Logger                { return logger{c} }

// Log levels in increasing severity (RFC 5424).
var logLevels = []string{"debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"} //nolint:gochecknoglobals // constant lookup table

type logger struct{ c *callContext }

func (l logger) Debug(msg string, kv ...any) { l.send("debug", msg, kv) }
func (l logger) Info(msg string, kv ...any)  { l.send("info", msg, kv) }
func (l logger) Warn(msg string, kv ...any)  { l.send("warning", msg, kv) }
func (l logger) Error(msg string, kv ...any) { l.send("error", msg, kv) }

// send logs only if the request carried a log level and the message is at or above it.
func (l logger) send(level, msg string, kv []any) {
	threshold := l.c.meta.LogLevel
	if threshold == "" || l.c.task != nil || slices.Index(logLevels, level) < slices.Index(logLevels, threshold) {
		return
	}
	data := any(msg)
	if len(kv) > 0 {
		fields := map[string]any{"message": msg}
		for i := 0; i+1 < len(kv); i += 2 {
			fields[fmt.Sprint(kv[i])] = kv[i+1]
		}
		data = fields
	}
	l.c.notify("notifications/message", map[string]any{"level": level, "data": data})
}

// Elicit asks the user for structured input; T describes the form (a flat struct).
// It returns the user's answer and what the user did: accept, decline or cancel.
func Elicit[T any](ctx Context, message string) (T, Action, error) {
	var zero T
	requested, err := schema.Form(reflect.TypeFor[T]())
	if err != nil {
		return zero, "", fmt.Errorf("elicitation form: %w", err)
	}
	action, content, err := ctx.elicitForm(message, requested)
	if err != nil || action != Accept {
		return zero, action, err
	}
	var out T
	if err := json.Unmarshal(content, &out); err != nil {
		return zero, action, fmt.Errorf("decode elicitation answer: %w", err)
	}
	return out, action, nil
}

// invalidLogLevel reports an unknown log level of a request.
func invalidLogLevel(level string) *rpc.Error {
	return rpc.InvalidParams(fmt.Sprintf("unknown log level %q", level))
}
