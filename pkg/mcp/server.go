// Package mcp is a Model Context Protocol server over Streamable HTTP or stdio, implementing the
// 2026-07-28 revision of the specification. Serve it as an http.Handler (remote clients) or with
// Server.ServeStdio (a client that starts the process).
//
// Handlers are plain Go functions that take a Context and a typed input and return a
// typed output; they are registered with AddTool, AddResource and AddPrompt. The
// gomcp tool generates this registration code from annotations.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/StevenCyb/GoMCP/internal/transport"
)

// Server is an MCP server; it is an http.Handler for the MCP endpoint.
type Server struct {
	info   Info
	opts   options
	limits Limits

	mu          sync.RWMutex
	tools       []*tool
	toolIdx     map[string]*tool
	statics     []*resource
	staticIdx   map[string]*resource
	templates   []*resource
	listers     []resourceLister
	prompts     []*prompt
	promptIdx   map[string]*prompt
	completions map[completionKey]CompleteFunc
	middleware  []Middleware

	transport *transport.Server
	stateKey  []byte

	// base is the parent context of background work such as tasks; Close cancels it.
	base context.Context //nolint:containedctx // the server's lifetime
	stop context.CancelFunc

	subs  subscribers
	tasks *taskStore
}

// NewServer creates a server.
func NewServer(info Info, opts ...Option) *Server {
	base, stop := context.WithCancel(context.Background())
	s := &Server{
		base:        base,
		stop:        stop,
		info:        info,
		toolIdx:     map[string]*tool{},
		staticIdx:   map[string]*resource{},
		promptIdx:   map[string]*prompt{},
		completions: map[completionKey]CompleteFunc{},
		tasks:       newTaskStore(),
	}
	s.opts.cacheScope = CachePrivate
	for _, opt := range opts {
		opt(&s.opts)
	}
	s.stateKey = newStateKey(s.opts.stateKey)
	s.limits = s.opts.limits.withDefaults()
	cfg := transport.Config{
		AllowedOrigins: s.opts.origins,
		KeepAlive:      s.opts.keepAlive,
		CheckHeaders:   s.checkParamHeaders,
	}
	if len(s.opts.auth) > 0 {
		cfg.Authenticate = s.authenticate
		cfg.Authorize = s.authorize
	}
	s.transport = transport.New(cfg, s.dispatch)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.transport.ServeHTTP(w, r) }

// ServeStdio serves the stdio transport: newline-delimited JSON-RPC messages on in and out, until in
// ends or ctx is cancelled. The caller is the process that started the server, so there is no
// authentication and scopes are not checked. Nothing but protocol messages may be written to out: log
// to stderr. It closes the server when it returns.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	defer s.Close()
	return s.transport.ServeStdio(ctx, in, out) //nolint:wrapcheck // the transport error is already descriptive
}

// Close ends the subscriptions and cancels running tasks. Call it before shutting the HTTP server down.
func (s *Server) Close() {
	s.stop()
	s.transport.Close()
}

// Use adds middleware that wraps every tool call, resource read, prompt get and completion.
func (s *Server) Use(mw ...Middleware) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.middleware = append(s.middleware, mw...)
}

func (s *Server) chainFor(h Handler, use []Middleware) Handler {
	s.mu.RLock()
	all := append(append([]Middleware(nil), s.middleware...), use...)
	s.mu.RUnlock()
	return chain(h, all...)
}

var errNameRequired = errors.New("mcp: name is required")

func duplicate(kind, name string) error {
	return fmt.Errorf("mcp: %s %q is already registered", kind, name)
}
