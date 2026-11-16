package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioSession runs ServeStdio on pipes and gives the test a way to talk to it.
type stdioSession struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan rpc.Message
	done  chan error
}

func startStdio(t *testing.T, d *fakeDispatcher) *stdioSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &stdioSession{t: t, in: inW, lines: make(chan rpc.Message, 64), done: make(chan error, 1)}
	srv := transport.New(transport.Config{}, d.request)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		s.done <- srv.ServeStdio(ctx, inR, outW)
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var m rpc.Message
			if assert.NoError(t, json.Unmarshal(sc.Bytes(), &m), sc.Text()) {
				s.lines <- m
			}
		}
		close(s.lines)
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
	})
	return s
}

func (s *stdioSession) send(method string, id any, params map[string]any) {
	s.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	raw, err := json.Marshal(msg)
	require.NoError(s.t, err)
	_, err = s.in.Write(append(raw, '\n'))
	require.NoError(s.t, err)
}

// request sends a request with the metadata every request carries.
func (s *stdioSession) request(method string, id any, params map[string]any) {
	s.t.Helper()
	p := map[string]any{keyMeta: map[string]any{metaV: version, metaC: map[string]any{}}}
	maps.Copy(p, params)
	s.send(method, id, p)
}

func (s *stdioSession) next() rpc.Message {
	s.t.Helper()
	select {
	case m, ok := <-s.lines:
		require.True(s.t, ok, "stdout closed")
		return m
	case <-time.After(5 * time.Second):
		require.FailNow(s.t, "timed out waiting for a message")
		return rpc.Message{}
	}
}

func TestStdioAnswersRequestsOneJSONObjectPerLine(t *testing.T) {
	s := startStdio(t, newFake())
	s.request("echo", 1, nil)
	m := s.next()
	assert.JSONEq(t, "1", string(m.ID))
	assert.JSONEq(t, `{"method":"echo"}`, string(m.Result))

	s.request("echo", "abc", nil)
	assert.JSONEq(t, `"abc"`, string(s.next().ID), "string ids work")
}

func TestStdioDoesNotNeedHTTPHeaders(t *testing.T) {
	s := startStdio(t, newFake())
	s.request("tools/call", 1, map[string]any{"name": "x"}) // over HTTP this needs Mcp-Method and Mcp-Name
	assert.Nil(t, s.next().Error)
}

func TestStdioValidatesTheMetadata(t *testing.T) {
	s := startStdio(t, newFake())
	s.send("echo", 1, nil)
	m := s.next()
	require.NotNil(t, m.Error)
	assert.Equal(t, rpc.CodeInvalidParams, m.Error.Code)

	s.send("echo", 2, map[string]any{keyMeta: map[string]any{metaV: "1999-01-01", metaC: map[string]any{}}})
	m = s.next()
	require.NotNil(t, m.Error)
	assert.Equal(t, rpc.CodeUnsupportedProtocol, m.Error.Code)

	s.send("initialize", 3, map[string]any{"protocolVersion": "2025-11-25"})
	m = s.next()
	require.NotNil(t, m.Error)
	assert.Equal(t, rpc.CodeUnsupportedProtocol, m.Error.Code)
}

func TestStdioReportsBadInputAndKeepsServing(t *testing.T) {
	s := startStdio(t, newFake())
	_, err := s.in.Write([]byte("not json\n"))
	require.NoError(t, err)
	m := s.next()
	require.NotNil(t, m.Error)
	assert.Equal(t, rpc.CodeParse, m.Error.Code)
	assert.JSONEq(t, "null", string(m.ID))

	_, err = s.in.Write([]byte("[{\"jsonrpc\":\"2.0\"}]\n"))
	require.NoError(t, err)
	m = s.next()
	require.NotNil(t, m.Error)
	assert.Equal(t, rpc.CodeInvalidRequest, m.Error.Code, "no batching")

	_, err = s.in.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":5,\"result\":{}}\n"))
	require.NoError(t, err)
	m = s.next()
	require.NotNil(t, m.Error, "clients send requests, not responses")

	_, err = s.in.Write([]byte("\n   \n"))
	require.NoError(t, err)
	s.request("echo", 9, nil)
	assert.JSONEq(t, "9", string(s.next().ID), "empty lines are skipped and the server still answers")
}

func TestStdioSendsNotificationsBeforeTheResponse(t *testing.T) {
	s := startStdio(t, newFake())
	s.request("stream", 1, nil)
	first, second, last := s.next(), s.next(), s.next()
	assert.Equal(t, "notifications/progress", first.Method)
	assert.Equal(t, "notifications/progress", second.Method)
	assert.JSONEq(t, "1", string(last.ID))
	assert.JSONEq(t, `{"done":"yes"}`, string(last.Result))
}

func TestStdioHandlesRequestsConcurrently(t *testing.T) {
	d := newFake()
	s := startStdio(t, d)
	s.request("slow", 1, nil)
	<-d.started
	s.request("echo", 2, nil) // answered while the slow one is still running
	assert.JSONEq(t, "2", string(s.next().ID))
}

func TestStdioCancelsARequestAndSendsNoResponse(t *testing.T) {
	d := newFake()
	s := startStdio(t, d)
	s.request("slow", 1, nil)
	<-d.started
	s.send("notifications/cancelled", nil, map[string]any{"requestId": 1, "reason": "user"})
	select {
	case <-d.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler context was not cancelled")
	}
	s.request("echo", 2, nil)
	assert.JSONEq(t, "2", string(s.next().ID), "the next message is not a response to the cancelled request")
}

func TestStdioEndsWhenInputCloses(t *testing.T) {
	d := newFake()
	s := startStdio(t, d)
	s.request("slow", 1, nil)
	<-d.started
	require.NoError(t, s.in.Close())
	select {
	case err := <-s.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return after end of input")
	}
	select {
	case <-d.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("running requests are cancelled at end of input")
	}
}

func TestStdioEndsWhenContextIsCancelled(t *testing.T) {
	inR, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	srv := transport.New(transport.Config{}, newFake().request)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.ServeStdio(ctx, inR, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio ignored the cancelled context")
	}
}

func TestStdioCallIsTrustedAndHasNoHTTPRequest(t *testing.T) {
	var (
		mu               sync.Mutex
		trusted, hasHTTP bool
		principal        any
	)
	srv := transport.New(transport.Config{}, func(c *transport.Call, _ transport.Request) (any, *rpc.Error) {
		mu.Lock()
		defer mu.Unlock()
		trusted, hasHTTP, principal = c.Trusted(), c.HTTPRequest() != nil, c.Principal()
		return map[string]bool{"ok": true}, nil
	})
	var out strings.Builder
	input := `{"jsonrpc":"2.0","id":1,"method":"x","params":{"_meta":{"` + metaV + `":"` + version + `","` + metaC + `":{}}}}` + "\n"
	require.NoError(t, srv.ServeStdio(t.Context(), strings.NewReader(input), &out))
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, trusted, "stdio callers are trusted: authentication is for HTTP")
	assert.False(t, hasHTTP)
	assert.Nil(t, principal)
	assert.Contains(t, out.String(), `"ok":true`)
}

func TestStdioRejectsTooLongLines(t *testing.T) {
	srv := transport.New(transport.Config{}, newFake().request)
	line := `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"pad":"` + strings.Repeat("a", 5<<20) + `"}}` + "\n"
	var out strings.Builder
	err := srv.ServeStdio(t.Context(), strings.NewReader(line), &out)
	require.Error(t, err)
	assert.Contains(t, out.String(), `"code":-32600`, "the client is told before the server stops")
}
