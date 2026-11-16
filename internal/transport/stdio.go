package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

const methodCancelled = "notifications/cancelled"

// errMessageTooLong is returned when a message is longer than the body limit of the HTTP transport.
var errMessageTooLong = errors.New("transport: message exceeds the size limit")

// stdioWriter writes one JSON object per line; calls write from several goroutines.
type stdioWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *stdioWriter) write(m rpc.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

// running is a request in flight that a notifications/cancelled can end.
type running struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

// ServeStdio serves the stdio transport: newline-delimited JSON-RPC messages on in and out.
// Requests run concurrently, a notifications/cancelled ends the request it names without a response,
// and the call returns when in is exhausted or ctx ends. There is no authentication: the caller is
// the process that started the server. Nothing but protocol messages may be written to out.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	w := &stdioWriter{w: out}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		runs = map[string]*running{}
	)
	defer wg.Wait() // runs last: the handlers have been cancelled by then
	defer cancel()

	lines, readErr := readLines(ctx, in)
	for {
		var line []byte
		select {
		case <-ctx.Done():
			return nil
		case l, ok := <-lines:
			if !ok {
				err := <-readErr
				if errors.Is(err, errMessageTooLong) {
					_ = w.write(rpc.NewError(json.RawMessage("null"), rpc.InvalidRequest("message too large")))
				}
				return err
			}
			line = l
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '[' {
			_ = w.write(rpc.NewError(json.RawMessage("null"), rpc.InvalidRequest("batching is not supported")))
			continue
		}
		var msg rpc.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			_ = w.write(rpc.NewError(json.RawMessage("null"), rpc.ParseError(err.Error())))
			continue
		}
		switch {
		case msg.IsNotification():
			if msg.Method == methodCancelled {
				cancelRun(&mu, runs, msg.Params)
			}
			continue
		case !msg.IsRequest():
			_ = w.write(rpc.NewError(json.RawMessage("null"), rpc.InvalidRequest("clients send requests, not responses")))
			continue
		}
		req, rpcErr := s.validate(nil, msg)
		if rpcErr != nil {
			_ = w.write(rpc.NewError(msg.ID, rpcErr))
			continue
		}

		callCtx, cancelCall := context.WithCancel(ctx)
		run := &running{cancel: cancelCall}
		key := string(bytes.TrimSpace(msg.ID))
		mu.Lock()
		runs[key] = run
		mu.Unlock()
		c := &Call{srv: s, ctx: callCtx, canSSE: true, req: req, stdio: w}
		wg.Go(func() {
			defer cancelCall()
			result, rpcErr := s.d(c, req)
			mu.Lock()
			delete(runs, key)
			mu.Unlock()
			if run.cancelled.Load() {
				return // the client gave up on this request
			}
			if rpcErr != nil {
				c.finish(rpc.NewError(msg.ID, rpcErr))
				return
			}
			c.finish(rpc.NewResult(msg.ID, result))
		})
	}
}

func cancelRun(mu *sync.Mutex, runs map[string]*running, params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	mu.Lock()
	run := runs[string(bytes.TrimSpace(p.RequestID))]
	mu.Unlock()
	if run != nil {
		run.cancelled.Store(true)
		run.cancel()
	}
}

// readLines reads trimmed lines from in until ctx ends. The error channel yields why it stopped: nil at the end of input.
// A read that is blocked on in cannot be interrupted, so that goroutine ends with the process.
func readLines(ctx context.Context, in io.Reader) (<-chan []byte, <-chan error) {
	lines, errc := make(chan []byte), make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64*1024), defaultMaxBodyBytes)
		for sc.Scan() {
			select {
			case lines <- bytes.Clone(bytes.TrimSpace(sc.Bytes())):
			case <-ctx.Done():
				return
			}
		}
		err := sc.Err()
		if errors.Is(err, bufio.ErrTooLong) {
			err = errMessageTooLong
		}
		errc <- err
	}()
	return lines, errc
}
