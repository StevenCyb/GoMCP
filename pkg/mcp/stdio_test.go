package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"maps"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioClient talks to Server.ServeStdio over pipes.
type stdioClient struct {
	t    *testing.T
	in   *io.PipeWriter
	out  chan rpc.Message
	done chan error
	id   int
	caps map[string]any
}

func serveStdio(t *testing.T, s *mcp.Server, caps map[string]any) *stdioClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &stdioClient{t: t, in: inW, out: make(chan rpc.Message, 64), done: make(chan error, 1), caps: caps}
	if c.caps == nil {
		c.caps = map[string]any{}
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		c.done <- s.ServeStdio(ctx, inR, outW)
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var m rpc.Message
			if assert.NoError(t, json.Unmarshal(sc.Bytes(), &m), sc.Text()) {
				c.out <- m
			}
		}
		close(c.out)
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
	})
	return c
}

func (c *stdioClient) send(method string, params map[string]any) int {
	c.t.Helper()
	c.id++
	meta := map[string]any{"io.modelcontextprotocol/protocolVersion": protocol, "io.modelcontextprotocol/clientCapabilities": c.caps}
	p := map[string]any{"_meta": meta}
	for k, v := range params {
		if k == "_meta" {
			given, _ := v.(map[string]any)
			maps.Copy(meta, given)
			continue
		}
		p[k] = v
	}
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": p})
	require.NoError(c.t, err)
	_, err = c.in.Write(append(raw, '\n'))
	require.NoError(c.t, err)
	return c.id
}

func (c *stdioClient) next() rpc.Message {
	c.t.Helper()
	select {
	case m, ok := <-c.out:
		require.True(c.t, ok, "stdout closed")
		return m
	case <-time.After(5 * time.Second):
		require.FailNow(c.t, "timed out waiting for a message")
		return rpc.Message{}
	}
}

// response waits for the response with the id; notifications on the way are returned too.
func (c *stdioClient) response(id int) (rpc.Message, []rpc.Message) {
	c.t.Helper()
	var notes []rpc.Message
	for {
		m := c.next()
		if m.IsNotification() {
			notes = append(notes, m)
			continue
		}
		require.JSONEq(c.t, string(rune('0'+id)), string(m.ID))
		return m, notes
	}
}

func stdioServer(t *testing.T, opts ...mcp.Option) *mcp.Server {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: toolHello, Scopes: []string{"admin"}},
		func(ctx mcp.Context, in struct {
			Name string `json:"name"`
		}) (string, error) {
			ctx.Log().Info("greeting", "name", in.Name)
			return "Hello " + in.Name, nil
		}))
	return s
}

func TestServeStdioCallsATool(t *testing.T) {
	c := serveStdio(t, stdioServer(t), nil)
	id := c.send("tools/call", map[string]any{keyName: toolHello, keyArgs: map[string]any{keyName: "world"}})
	m, notes := c.response(id)
	require.Nil(t, m.Error)
	assert.Equal(t, "Hello world", result[toolResult](t, m).Content[0][keyText], "scoped tools work: stdio callers are trusted")
	assert.Empty(t, notes, "no log notification without a log level in the request")
}

func TestServeStdioSendsLogsWhenAsked(t *testing.T) {
	c := serveStdio(t, stdioServer(t), nil)
	id := c.send("tools/call", map[string]any{
		keyName: toolHello, keyArgs: map[string]any{keyName: "x"},
		"_meta": map[string]any{"io.modelcontextprotocol/logLevel": "info"},
	})
	_, notes := c.response(id)
	require.Len(t, notes, 1)
	assert.Equal(t, "notifications/message", notes[0].Method)
}

func TestServeStdioDiscover(t *testing.T) {
	c := serveStdio(t, stdioServer(t), nil)
	m, _ := c.response(c.send("server/discover", nil))
	require.Nil(t, m.Error)
	res := result[discoverResult](t, m)
	assert.Equal(t, []string{protocol}, res.SupportedVersions)
}

func TestServeStdioAsksTheClientForInput(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "where"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		roots, err := ctx.Roots()
		if err != nil {
			return "", err
		}
		return roots[0].URI, nil
	}))
	c := serveStdio(t, s, map[string]any{mcp.CapRoots: map[string]any{}})

	first, _ := c.response(c.send("tools/call", map[string]any{keyName: "where"}))
	head := result[struct {
		ResultType    string         `json:"resultType"`
		InputRequests map[string]any `json:"inputRequests"`
		RequestState  string         `json:"requestState"`
	}](t, first)
	require.Equal(t, "input_required", head.ResultType)
	answers := map[string]any{}
	for key := range head.InputRequests {
		answers[key] = map[string]any{mcp.CapRoots: []any{map[string]any{keyURI: "file:///w"}}}
	}
	second, _ := c.response(c.send("tools/call", map[string]any{keyName: "where", "inputResponses": answers, keyState: head.RequestState}))
	require.Nil(t, second.Error)
	assert.Equal(t, "file:///w", result[toolResult](t, second).Content[0][keyText])
}

func TestServeStdioListenStaysOpenAndDeliversUpdates(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: "secret:///{id}", Name: "s", Subscribable: true, Scopes: []string{"admin"}},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("x")}, nil }))
	c := serveStdio(t, s, nil)

	listenID := c.send("subscriptions/listen", map[string]any{"notifications": map[string]any{"resourceSubscriptions": []string{"secret:///1"}}})
	ack := c.next()
	assert.Equal(t, "notifications/subscriptions/acknowledged", ack.Method)
	assert.JSONEq(t, `{"resourceSubscriptions":["secret:///1"]}`, string(rpc.MustJSON(mustField(t, ack.Params, "notifications"))),
		"the scoped resource is acknowledged: stdio callers are trusted")

	s.ResourceUpdated("secret:///1")
	update := c.next()
	assert.Equal(t, "notifications/resources/updated", update.Method)

	// the listen request ends when the client cancels it
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": listenID}})
	require.NoError(t, err)
	_, err = c.in.Write(append(raw, '\n'))
	require.NoError(t, err)
	other := c.send("tools/list", nil)
	m, _ := c.response(other)
	assert.Nil(t, m.Error, "the server keeps serving after the listen was cancelled")
}

func TestServeStdioDropsRelativeIconsWithoutAHost(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{
		Name: "t", Icons: []mcp.Icon{{Src: "assets/a.svg"}, {Src: "https://example.com/b.png"}},
	}, func(mcp.Context, mcp.Empty) (string, error) { return "", nil }))
	c := serveStdio(t, s, nil)

	m, _ := c.response(c.send("tools/list", nil))
	var list struct {
		Tools []struct {
			Icons []mcp.Icon `json:"icons"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(m.Result, &list))
	require.Len(t, list.Tools, 1)
	require.Len(t, list.Tools[0].Icons, 1, "there is no server URL over stdio, so relative icons cannot be resolved")
	assert.Equal(t, "https://example.com/b.png", list.Tools[0].Icons[0].Src)
}

func TestServeStdioStopsWhenInputEnds(t *testing.T) {
	c := serveStdio(t, stdioServer(t), nil)
	require.NoError(t, c.in.Close())
	select {
	case err := <-c.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return")
	}
}
