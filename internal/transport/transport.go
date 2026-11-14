// Package transport implements the MCP Streamable HTTP transport of the 2026-07-28 revision:
// one POST endpoint, no sessions, every request carries its protocol version and client
// capabilities, and a response is plain JSON or an SSE stream.
package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

// Version is the protocol revision this transport implements.
const Version = "2026-07-28"

const (
	headerVersion = "Mcp-Protocol-Version" // canonical form of MCP-Protocol-Version
	headerMethod  = "Mcp-Method"
	headerName    = "Mcp-Name"

	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaLogLevel     = "io.modelcontextprotocol/logLevel"

	defaultKeepAlive    = 20 * time.Second
	defaultMaxBodyBytes = 4 << 20
	methodInitialize    = "initialize"
	base64Prefix        = "=?base64?"
	base64Suffix        = "?="
)

// SupportedVersions lists the protocol versions this transport accepts.
func SupportedVersions() []string { return []string{Version} }

// Meta is the _meta of a request.
type Meta struct {
	ProtocolVersion string
	Capabilities    json.RawMessage // ClientCapabilities, may be {}
	LogLevel        string
	ProgressToken   json.RawMessage
}

// Request is a validated client request.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Meta   Meta
}

// Dispatcher handles a validated request and returns its result or a protocol error.
type Dispatcher func(c *Call, req Request) (any, *rpc.Error)

// Config configures the transport.
type Config struct {
	// AllowedOrigins lists accepted Origin header values. Empty accepts only localhost origins.
	AllowedOrigins []string
	// KeepAlive is the interval of SSE keep-alive comments; zero means 20 seconds.
	KeepAlive time.Duration
	// Authenticate checks the credentials of every request and returns the caller;
	// nil disables authentication.
	Authenticate func(r *http.Request) (principal any, fail *AuthFailure)
	// Authorize checks a decoded request before it is dispatched; nil allows everything.
	Authorize func(c *Call, method string, params json.RawMessage) *AuthFailure
	// CheckHeaders validates further headers of a request, such as the Mcp-Param-* headers of tools.
	CheckHeaders func(h http.Header, req Request) *rpc.Error
}

// AuthFailure rejects a request: Status is 401 or 403 and Challenge the WWW-Authenticate value.
type AuthFailure struct {
	Status    int
	Challenge string
}

// Server is the http.Handler of the MCP endpoint.
type Server struct {
	cfg Config
	d   Dispatcher

	closed    chan struct{}
	closeOnce sync.Once
}

// New creates the transport.
func New(cfg Config, d Dispatcher) *Server {
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = defaultKeepAlive
	}
	return &Server{cfg: cfg, d: d, closed: make(chan struct{})}
}

// Close tells long running calls (subscriptions) to end.
func (s *Server) Close() { s.closeOnce.Do(func() { close(s.closed) }) }

type principalKey struct{}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, rpc.InvalidRequest("origin not allowed"))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var principal any
	if s.cfg.Authenticate != nil {
		var fail *AuthFailure
		if principal, fail = s.cfg.Authenticate(r); fail != nil {
			writeAuthFailure(w, fail)
			return
		}
	}
	s.post(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
}

func writeAuthFailure(w http.ResponseWriter, fail *AuthFailure) {
	if fail.Challenge != "" {
		w.Header().Set("WWW-Authenticate", fail.Challenge)
	}
	msg := "authentication required"
	if fail.Status == http.StatusForbidden {
		msg = "insufficient permissions"
	}
	writeError(w, fail.Status, rpc.InvalidRequest(msg))
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(s.cfg.AllowedOrigins) > 0 {
		return slices.Contains(s.cfg.AllowedOrigins, origin)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func acceptsSSE(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/event-stream") || strings.Contains(accept, "*/*")
}

func writeJSON(w http.ResponseWriter, status int, m rpc.Message) {
	b, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeError(w http.ResponseWriter, status int, e *rpc.Error) {
	writeJSON(w, status, rpc.NewError(json.RawMessage("null"), e))
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, defaultMaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, rpc.InvalidRequest("request body too large"))
			return
		}
		writeError(w, http.StatusBadRequest, rpc.ParseError(err.Error()))
		return
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		writeError(w, http.StatusBadRequest, rpc.InvalidRequest("batching is not supported"))
		return
	}
	var msg rpc.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		writeError(w, http.StatusBadRequest, rpc.ParseError(err.Error()))
		return
	}
	switch {
	case msg.IsNotification():
		w.WriteHeader(http.StatusAccepted) // nothing to do: the only client notification is stdio only
		return
	case !msg.IsRequest():
		writeError(w, http.StatusBadRequest, rpc.InvalidRequest("clients send requests, not responses"))
		return
	}

	req, rpcErr := s.validate(r, msg)
	if rpcErr != nil {
		writeJSON(w, http.StatusBadRequest, rpc.NewError(msg.ID, rpcErr))
		return
	}
	fl, _ := w.(http.Flusher)
	c := &Call{srv: s, ctx: r.Context(), r: r, w: w, fl: fl, canSSE: fl != nil && acceptsSSE(r), req: req}
	if s.cfg.Authorize != nil {
		if fail := s.cfg.Authorize(c, req.Method, req.Params); fail != nil {
			writeAuthFailure(w, fail)
			return
		}
	}
	result, rpcErr := s.d(c, req)
	if rpcErr != nil {
		c.finish(rpc.NewError(msg.ID, rpcErr))
		return
	}
	c.finish(rpc.NewResult(msg.ID, result))
}

// validate checks everything about a request that does not need to know the method:
// _meta, protocol version and the standard headers.
func (s *Server) validate(r *http.Request, msg rpc.Message) (Request, *rpc.Error) {
	if msg.Method == methodInitialize {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		return Request{}, rpc.UnsupportedVersion(SupportedVersions(), p.ProtocolVersion)
	}
	meta, rpcErr := parseMeta(msg.Params)
	if rpcErr != nil {
		return Request{}, rpcErr
	}
	req := Request{ID: msg.ID, Method: msg.Method, Params: msg.Params, Meta: meta}

	if !slices.Contains(SupportedVersions(), meta.ProtocolVersion) {
		return Request{}, rpc.UnsupportedVersion(SupportedVersions(), meta.ProtocolVersion)
	}
	if got := r.Header.Get(headerVersion); got != meta.ProtocolVersion {
		return Request{}, rpc.HeaderMismatch(headerMismatch(headerVersion, got, meta.ProtocolVersion))
	}
	if got := r.Header.Get(headerMethod); got != msg.Method {
		return Request{}, rpc.HeaderMismatch(headerMismatch(headerMethod, got, msg.Method))
	}
	if field := nameField(msg.Method); field != "" {
		want := paramString(msg.Params, field)
		got, ok := DecodeHeaderValue(r.Header.Get(headerName))
		if !ok || got != want {
			return Request{}, rpc.HeaderMismatch(headerMismatch(headerName, r.Header.Get(headerName), want))
		}
	}
	if s.cfg.CheckHeaders != nil {
		if rpcErr := s.cfg.CheckHeaders(r.Header, req); rpcErr != nil {
			return Request{}, rpcErr
		}
	}
	return req, nil
}

func headerMismatch(name, got, want string) string {
	if got == "" {
		return "Header mismatch: " + name + " header is missing, the request says '" + want + "'"
	}
	return "Header mismatch: " + name + " header value '" + got + "' does not match body value '" + want + "'"
}

// nameField returns the parameter that must also be sent as Mcp-Name.
func nameField(method string) string {
	switch method {
	case "tools/call", "prompts/get":
		return "name"
	case "resources/read":
		return "uri"
	case "tasks/get", "tasks/update", "tasks/cancel":
		return "taskId"
	default:
		return ""
	}
}

func paramString(params json.RawMessage, field string) string {
	var p map[string]json.RawMessage
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(p[field], &s)
	return s
}

// DecodeHeaderValue undoes the =?base64?...?= encoding of header values.
func DecodeHeaderValue(v string) (string, bool) {
	inner, ok := strings.CutPrefix(v, base64Prefix)
	if !ok {
		return v, true
	}
	inner, ok = strings.CutSuffix(inner, base64Suffix)
	if !ok {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// parseMeta reads and checks the required _meta fields of a request.
func parseMeta(params json.RawMessage) (Meta, *rpc.Error) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return Meta{}, rpc.InvalidParams("invalid params: " + err.Error())
		}
	}
	var m Meta
	if raw, ok := p.Meta[metaVersion]; !ok || json.Unmarshal(raw, &m.ProtocolVersion) != nil || m.ProtocolVersion == "" {
		return Meta{}, rpc.InvalidParams("params._meta is missing " + metaVersion)
	}
	caps, ok := p.Meta[metaCapabilities]
	if !ok || !bytes.HasPrefix(bytes.TrimSpace(caps), []byte("{")) {
		return Meta{}, rpc.InvalidParams("params._meta is missing " + metaCapabilities)
	}
	m.Capabilities = caps
	_ = json.Unmarshal(p.Meta[metaLogLevel], &m.LogLevel)
	m.ProgressToken = p.Meta["progressToken"]
	return m, nil
}
