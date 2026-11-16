package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

var (
	errCallDone = errors.New("transport: call already finished")
	errNoStream = errors.New("transport: client does not accept text/event-stream")
)

// Call is one in-flight client request. Notifications made through it upgrade the HTTP
// response to an SSE stream on first use; the final response ends the stream.
type Call struct {
	srv    *Server
	ctx    context.Context //nolint:containedctx // a Call is request scoped, like http.Request
	r      *http.Request
	w      http.ResponseWriter
	fl     http.Flusher
	canSSE bool
	req    Request
	stdio  *stdioWriter // set for calls over stdio: there is no HTTP request or response

	mu        sync.Mutex
	streaming bool
	done      bool
	stopPing  chan struct{}
}

// Context ends when the client closes the connection: that is how a request is cancelled.
func (c *Call) Context() context.Context { return c.ctx }

// Principal returns what Config.Authenticate returned for the request; nil without authentication
// and for calls over stdio.
func (c *Call) Principal() any {
	if c.r == nil {
		return nil
	}
	return c.r.Context().Value(principalKey{})
}

// Trusted reports whether the caller needs no authentication: a call over stdio comes from the
// process that started the server.
func (c *Call) Trusted() bool { return c.stdio != nil }

// HTTPRequest returns the underlying HTTP request (headers, TLS state, ...); nil for calls over stdio.
func (c *Call) HTTPRequest() *http.Request { return c.r }

// Meta returns the validated _meta of the request.
func (c *Call) Meta() Meta { return c.req.Meta }

// ID returns the JSON-RPC id of the request.
func (c *Call) ID() json.RawMessage { return c.req.ID }

// CanStream reports whether the client accepts an SSE response.
func (c *Call) CanStream() bool { return c.canSSE }

// Closing is closed when the server shuts down; long running calls should end then.
func (c *Call) Closing() <-chan struct{} { return c.srv.closed }

func (c *Call) send(m rpc.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return errCallDone
	}
	if c.stdio != nil {
		return c.stdio.write(m)
	}
	if !c.streaming {
		if !c.canSSE {
			return errNoStream
		}
		h := c.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		c.w.WriteHeader(http.StatusOK)
		c.streaming = true
		c.stopPing = make(chan struct{})
		go c.keepAlive(c.stopPing)
	}
	return c.writeEvent(m)
}

func (c *Call) writeEvent(m rpc.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err //nolint:wrapcheck // a plain marshal error
	}
	if _, err := c.w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
		return err //nolint:wrapcheck // the connection is gone
	}
	c.fl.Flush()
	return nil
}

// keepAlive sends SSE comments so proxies do not close a quiet stream.
func (c *Call) keepAlive(stop <-chan struct{}) {
	ticker := time.NewTicker(c.srv.cfg.KeepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			if !c.done {
				_, _ = c.w.Write([]byte(": ping\n\n"))
				c.fl.Flush()
			}
			c.mu.Unlock()
		case <-stop:
			return
		}
	}
}

// Notify sends a notification on the response stream of this call. It is best effort: when the
// client does not accept SSE or the call is finished the notification is dropped.
func (c *Call) Notify(method string, params any) bool {
	return c.send(rpc.NewNotification(method, params)) == nil
}

// finish writes the final response, as one SSE event if the call is streaming, else as JSON.
func (c *Call) finish(m rpc.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	c.done = true
	if c.stdio != nil {
		_ = c.stdio.write(m)
		return
	}
	if c.streaming {
		close(c.stopPing)
		_ = c.writeEvent(m)
		return
	}
	status := http.StatusOK
	if m.Error != nil {
		status = statusFor(m.Error.Code)
	}
	writeJSON(c.w, status, m)
}

// statusFor maps the JSON-RPC errors that are about the request itself to their HTTP status.
func statusFor(code int) int {
	switch code {
	case rpc.CodeHeaderMismatch, rpc.CodeMissingCapability, rpc.CodeUnsupportedProtocol:
		return http.StatusBadRequest
	case rpc.CodeMethodNotFound:
		return http.StatusNotFound
	default:
		return http.StatusOK
	}
}
