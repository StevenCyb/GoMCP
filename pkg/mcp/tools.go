package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/schema"
	"github.com/StevenCyb/GoMCP/internal/transport"
)

// Hint carries the optional tool annotations. Only hints that are set are sent;
// clients treat unset ones as unknown (and a tool as potentially destructive).
type Hint struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

func (h Hint) annotations() map[string]any {
	out := map[string]any{}
	if h.ReadOnly {
		out["readOnlyHint"] = true
	}
	if h.Destructive {
		out["destructiveHint"] = true
	}
	if h.Idempotent {
		out["idempotentHint"] = true
	}
	if h.OpenWorld {
		out["openWorldHint"] = true
	}
	return out
}

// TaskMode says whether a tool can run as a task.
type TaskMode string

// Task modes.
const (
	TaskForbidden TaskMode = "forbidden"
	TaskOptional  TaskMode = "optional"
	TaskRequired  TaskMode = "required"
)

// ToolDef describes a tool.
type ToolDef struct {
	Name        string
	Title       string
	Description string
	Icons       []Icon
	Hint        Hint
	Meta        map[string]any
	Scopes      []string
	// Requires lists client capabilities the tool needs (CapSampling, CapRoots, ...).
	Requires []string
	Use      []Middleware
	Task     TaskMode
	TaskTTL  time.Duration
	TaskPoll time.Duration
}

type outputMode int

const (
	outText       outputMode = iota // string, number, list: text content only
	outStructured                   // struct: structuredContent plus text
	outNone                         // Empty: no content
)

type toolOutput struct {
	mode       outputMode
	structured any
	content    []Content
}

type tool struct {
	def          ToolDef
	inputSchema  map[string]any
	outputSchema map[string]any
	headers      []schema.HeaderParam
	invoke       func(Context, json.RawMessage) (toolOutput, error)
}

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// AddTool registers a tool. In must be a struct (its schema is the input schema);
// a struct Out also gives the output schema and structuredContent.
func AddTool[In, Out any](s *Server, def ToolDef, h func(Context, In) (Out, error)) error {
	if def.Name == "" {
		return errNameRequired
	}
	if !toolNamePattern.MatchString(def.Name) {
		return fmt.Errorf("tool name %q must be 1 to 128 characters of A-Z a-z 0-9 _ . -", def.Name)
	}
	inType := reflect.TypeFor[In]()
	inSchema, isObject, err := schema.ObjectFor(inType)
	if err != nil {
		return fmt.Errorf("tool %q input: %w", def.Name, err)
	}
	if !isObject {
		return fmt.Errorf("tool %q: input must be a struct, got %s", def.Name, inType)
	}
	if inType == reflect.TypeFor[Empty]() {
		inSchema = map[string]any{"type": "object", "additionalProperties": false} // a tool without parameters
	}
	mode, outSchema, err := outputInfo(reflect.TypeFor[Out]())
	if err != nil {
		return fmt.Errorf("tool %q output: %w", def.Name, err)
	}
	validate := inType != reflect.TypeFor[Empty]()

	t := &tool{def: def, inputSchema: inSchema, outputSchema: outSchema, headers: schema.HeaderParams(inSchema)}
	t.invoke = func(ctx Context, args json.RawMessage) (toolOutput, error) {
		var in In
		if validate {
			if err := decodeArguments(inSchema, args, &in); err != nil {
				return toolOutput{}, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		out, err := h(ctx, in)
		if err != nil {
			return toolOutput{}, err
		}
		return packOutput(mode, out)
	}
	return s.addTool(t)
}

func (s *Server) addTool(t *tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.toolIdx[t.def.Name]; ok {
		return duplicate("tool", t.def.Name)
	}
	s.toolIdx[t.def.Name] = t
	s.tools = append(s.tools, t)
	return nil
}

// outputInfo decides how a result type is sent and derives its output schema: structs, lists and
// maps are structured content, other values are text, Empty is nothing.
func outputInfo(t reflect.Type) (outputMode, map[string]any, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct && t.Implements(reflect.TypeFor[resultValue]()) {
		if field, ok := t.FieldByName("Structured"); ok {
			t = field.Type
		}
	}
	if t == reflect.TypeFor[Empty]() {
		return outNone, nil, nil
	}
	switch t.Kind() { //nolint:exhaustive // every other kind is a plain value
	case reflect.Struct, reflect.Slice, reflect.Array, reflect.Map:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return outText, nil, nil // []byte
		}
		s, err := schema.For(t)
		if err != nil {
			return 0, nil, fmt.Errorf("schema: %w", err)
		}
		return outStructured, s, nil
	default:
		return outText, nil, nil
	}
}

func packOutput(mode outputMode, out any) (toolOutput, error) {
	var extra []Content
	if rv, ok := out.(resultValue); ok {
		out, extra = rv.structured(), rv.extra()
	}
	switch mode {
	case outNone:
		return toolOutput{mode: outNone, content: extra}, nil
	case outText:
		return packText(out, extra)
	case outStructured:
		out = nonNil(out)
		text, err := json.Marshal(out)
		if err != nil {
			return toolOutput{}, fmt.Errorf("encode result: %w", err)
		}
		content := extra
		if len(content) == 0 {
			content = []Content{Text(string(text))}
		}
		return toolOutput{mode: outStructured, structured: out, content: content}, nil
	}
	return toolOutput{}, fmt.Errorf("unknown output mode %d", mode)
}

func packText(out any, extra []Content) (toolOutput, error) {
	if len(extra) > 0 {
		return toolOutput{mode: outText, content: extra}, nil
	}
	if s, ok := out.(string); ok {
		return toolOutput{mode: outText, content: []Content{Text(s)}}, nil
	}
	text, err := json.Marshal(out)
	if err != nil {
		return toolOutput{}, fmt.Errorf("encode result: %w", err)
	}
	return toolOutput{mode: outText, content: []Content{Text(string(text))}}, nil
}

// decodeArguments validates the raw arguments against the input schema and decodes them.
func decodeArguments(inSchema map[string]any, args json.RawMessage, dst any) error {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	var generic any
	if err := json.Unmarshal(args, &generic); err != nil {
		return fmt.Errorf("arguments are not valid JSON: %w", err)
	}
	if err := schema.Validate(inSchema, generic); err != nil {
		return err
	}
	if err := json.Unmarshal(args, dst); err != nil {
		return fmt.Errorf("decode arguments: %w", err)
	}
	return nil
}

type toolEntry struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Icons        []Icon         `json:"icons,omitempty"`
	Meta         map[string]any `json:"_meta,omitempty"` //nolint:tagliatelle // field name defined by the MCP specification
}

func (s *Server) listTools(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	req, rpcErr := s.pageRequest(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.RLock()
	entries := make([]toolEntry, 0, len(s.tools))
	for _, t := range s.tools {
		entries = append(entries, toolEntry{
			Name: t.def.Name, Title: t.def.Title, Description: t.def.Description,
			InputSchema: t.inputSchema, OutputSchema: t.outputSchema,
			Annotations: nilIfEmpty(t.def.Hint.annotations()), Icons: s.resolveIcons(t.def.Icons, ctx.call.HTTPRequest()), Meta: t.def.Meta,
		})
	}
	s.mu.RUnlock()
	page := Paginate(entries, req)
	return listResult("tools", page.Items, page.Next), nil
}

func nilIfEmpty(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	return m
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type callResult struct {
	Content           []Content      `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"` //nolint:tagliatelle // field name defined by the MCP specification
}

func (s *Server) callTool(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, rpc.InvalidParams("invalid tool call: " + err.Error())
	}
	s.mu.RLock()
	t := s.toolIdx[p.Name]
	s.mu.RUnlock()
	if t == nil {
		return nil, rpc.InvalidParams("unknown tool " + p.Name)
	}
	if rpcErr := ctx.loadInput("tools/call", params); rpcErr != nil {
		return nil, rpcErr
	}
	if missing := missingFor(ctx.caps, t.def.Requires); len(missing) > 0 {
		return nil, rpc.MissingCapability(capabilityObject(missing...))
	}

	if t.def.Task == TaskOptional || t.def.Task == TaskRequired {
		switch {
		case ctx.caps.hasExtension(tasksExtension):
			return s.startTask(ctx, t, params)
		case t.def.Task == TaskRequired:
			return nil, rpc.MissingCapability(extensionObject(tasksExtension))
		}
	}

	out, err := s.chainFor(s.toolCore(t), t.def.Use)(ctx, Request{Kind: KindTool, Name: p.Name, Params: params})
	if len(ctx.asked) > 0 {
		return ctx.inputRequired(), nil
	}
	return toolResult(out, err)
}

// missingFor returns the capabilities of names that the client did not declare.
func missingFor(caps clientCaps, names []string) []string {
	var missing []string
	for _, name := range names {
		if !caps.supports(name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// toolCore is the innermost handler of a tool call: the tool itself.
func (s *Server) toolCore(t *tool) Handler {
	return func(c Context, req Request) (any, error) {
		var args json.RawMessage
		var raw callParams
		if json.Unmarshal(req.Params, &raw) == nil {
			args = raw.Arguments
		}
		return t.invoke(c, args)
	}
}

// checkParamHeaders validates the Mcp-Param-* headers of a tools/call: every argument with an
// x-mcp-header must travel as that header with the same value, and null or absent ones must not.
func (s *Server) checkParamHeaders(h http.Header, req transport.Request) *rpc.Error {
	if req.Method != "tools/call" {
		return nil
	}
	var p callParams
	if json.Unmarshal(req.Params, &p) != nil {
		return nil //nolint:nilerr // malformed params are reported when the call is decoded
	}
	s.mu.RLock()
	t := s.toolIdx[p.Name]
	s.mu.RUnlock()
	if t == nil || len(t.headers) == 0 {
		return nil
	}
	var args map[string]json.RawMessage
	_ = json.Unmarshal(p.Arguments, &args)
	for _, hp := range t.headers {
		name := "Mcp-Param-" + hp.Header
		got, present := h.Get(name), h.Get(name) != ""
		raw, hasArg := args[hp.Prop]
		if !hasArg || string(raw) == "null" {
			if present {
				return rpc.HeaderMismatch("Header mismatch: " + name + " must be omitted when the argument is absent")
			}
			continue
		}
		want := headerText(raw, hp.Type)
		decoded, ok := transport.DecodeHeaderValue(got)
		if !present || !ok || !sameHeaderValue(decoded, want, hp.Type) {
			return rpc.HeaderMismatch("Header mismatch: " + name + " header value '" + got + "' does not match the argument '" + want + "'")
		}
	}
	return nil
}

// headerText renders an argument the way it travels in a header.
func headerText(raw json.RawMessage, typ string) string {
	if typ == "string" {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	return strings.TrimSpace(string(raw))
}

func sameHeaderValue(got, want, typ string) bool {
	if typ == "integer" {
		a, errA := strconv.ParseFloat(got, 64)
		b, errB := strconv.ParseFloat(want, 64)
		return errA == nil && errB == nil && a == b
	}
	return got == want
}

// toolResult maps a handler outcome to a tools/call result: protocol errors stay
// protocol errors, every other error is an execution error with isError: true.
func toolResult(out any, err error) (any, *rpc.Error) {
	if err != nil {
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) {
			return nil, rpcErr
		}
		return callResult{Content: []Content{Text(err.Error())}, IsError: true}, nil
	}
	o, ok := out.(toolOutput)
	if !ok {
		return nil, rpc.Internal(fmt.Sprintf("tool returned %T", out))
	}
	res := callResult{Content: o.content}
	if res.Content == nil {
		res.Content = []Content{}
	}
	if o.mode == outStructured {
		res.StructuredContent = o.structured
	}
	return res, nil
}

// nonNil turns a nil slice or map into an empty one, so structured content stays valid JSON for its schema.
func nonNil(v any) any {
	rv := reflect.ValueOf(v)
	switch {
	case rv.Kind() == reflect.Slice && rv.IsNil():
		return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	case rv.Kind() == reflect.Map && rv.IsNil():
		return reflect.MakeMap(rv.Type()).Interface()
	default:
		return v
	}
}
