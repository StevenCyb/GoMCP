package mcp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/require"
)

const (
	protocol            = "2026-07-28"
	eventPrefix         = "data: "
	keyName             = "name"
	keyAction           = "action"
	valAccept           = "accept"
	keyURL              = "url"
	refPrompt           = "ref/prompt"
	keyToolsListChanged = "toolsListChanged"
	headerRegion        = "Mcp-Param-Region"
	headerCount         = "Mcp-Param-X-Count"
	keyType             = "type"
	keyURI              = "uri"
	keyTeam             = "team"
	valDocs             = "docs"
	roleAssistant       = "assistant"
	mimePNG             = "image/png"
	idNote1             = "note-1"

	promptSummarize = "summarize"
	metaVersion     = "io.modelcontextprotocol/protocolVersion"
	metaCaps        = "io.modelcontextprotocol/clientCapabilities"
	metaLogLevel    = "io.modelcontextprotocol/logLevel"
)

// testClient is a minimal client of the 2026-07-28 protocol. Its call method also plays the
// part of a real client in the multi round-trip loop: it answers input requests and retries.
type testClient struct {
	t     *testing.T
	url   string
	token string
	caps  map[string]any
	// logLevel and progressToken are put into the _meta of every request when set.
	logLevel      string
	progressToken any
	// onRequest answers the input requests of a server (sampling, elicitation, roots).
	onRequest func(rpc.Message) (result any, rpcErr *rpc.Error)
	// notifications received on response streams, in order.
	notifications []rpc.Message
	nextID        int
	srv           *httptest.Server
	// rounds counts the retries of the last call.
	rounds int
}

const (
	keyResourceSubs  = "resourceSubscriptions"
	keyNotifications = "notifications"
)

// newServer starts s behind an httptest server.
func newServer(t *testing.T, s *mcp.Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(func() {
		s.Close()
		srv.Close()
	})
	return srv
}

// connect creates a client that declares the given capabilities with every request.
func connect(t *testing.T, srv *httptest.Server, caps map[string]any) *testClient {
	t.Helper()
	return connectAs(t, srv, "", caps)
}

// connectAs is connect with a bearer token.
func connectAs(t *testing.T, srv *httptest.Server, token string, caps map[string]any) *testClient {
	t.Helper()
	if caps == nil {
		caps = map[string]any{}
	}
	return &testClient{t: t, url: srv.URL, token: token, caps: caps, srv: srv}
}

// nameOf is the value of the Mcp-Name header of a request.
func nameOf(method string, params map[string]any) string {
	for _, field := range []string{"name", "uri", "taskId"} {
		if v, ok := params[field].(string); ok && (method == "tools/call" || method == "prompts/get" || method == "resources/read" || strings.HasPrefix(method, "tasks/")) {
			return v
		}
	}
	return ""
}

// envelope builds the request body with the required _meta.
func (c *testClient) envelope(method string, params map[string]any) (int, []byte) {
	c.nextID++
	p := map[string]any{}
	maps.Copy(p, params)
	meta := map[string]any{metaVersion: protocol, metaCaps: c.caps}
	if c.logLevel != "" {
		meta[metaLogLevel] = c.logLevel
	}
	if c.progressToken != nil {
		meta["progressToken"] = c.progressToken
	}
	if extra, ok := p["_meta"].(map[string]any); ok {
		maps.Copy(meta, extra)
	}
	p["_meta"] = meta
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": p})
	require.NoError(c.t, err)
	return c.nextID, body
}

// post sends one request and returns the HTTP response.
func (c *testClient) post(method string, params map[string]any, headers map[string]string) *http.Response {
	c.t.Helper()
	_, body := c.envelope(method, params)
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", protocol)
	req.Header.Set("Mcp-Method", method)
	if name := nameOf(method, params); name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	return resp
}

// once sends one request and returns the final message of its response.
func (c *testClient) once(method string, params map[string]any, headers map[string]string) rpc.Message {
	c.t.Helper()
	resp := c.post(method, params, headers)
	defer func() { _ = resp.Body.Close() }()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, err := io.ReadAll(resp.Body)
		require.NoError(c.t, err)
		var out rpc.Message
		require.NoError(c.t, json.Unmarshal(b, &out), string(b))
		return out
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), eventPrefix)
		if !ok || data == "" {
			continue
		}
		var m rpc.Message
		require.NoError(c.t, json.Unmarshal([]byte(data), &m))
		if m.IsNotification() {
			c.notifications = append(c.notifications, m)
			continue
		}
		return m
	}
	require.FailNow(c.t, "stream ended without a response")
	return rpc.Message{}
}

// call sends a request; while the server answers with input_required it answers the input
// requests through onRequest and retries, like a client does.
func (c *testClient) call(method string, params any) rpc.Message {
	c.t.Helper()
	p, _ := params.(map[string]any)
	if p == nil && params != nil {
		raw, err := json.Marshal(params)
		require.NoError(c.t, err)
		require.NoError(c.t, json.Unmarshal(raw, &p))
	}
	c.rounds = 0
	for {
		res := c.once(method, p, nil)
		if res.Error != nil {
			return res
		}
		var head struct {
			ResultType    string                    `json:"resultType"`
			InputRequests map[string]map[string]any `json:"inputRequests"`
			RequestState  string                    `json:"requestState"`
		}
		require.NoError(c.t, json.Unmarshal(res.Result, &head))
		if head.ResultType != "input_required" {
			return res
		}
		c.rounds++
		require.LessOrEqual(c.t, c.rounds, 6, "too many rounds")
		responses := map[string]any{}
		for key, ir := range head.InputRequests {
			require.NotNil(c.t, c.onRequest, "unexpected input request %v", ir["method"])
			raw, err := json.Marshal(ir["params"])
			require.NoError(c.t, err)
			reply, rpcErr := c.onRequest(rpc.Message{Method: ir["method"].(string), Params: raw})
			require.Nil(c.t, rpcErr)
			responses[key] = reply
		}
		next := map[string]any{}
		maps.Copy(next, p)
		next["inputResponses"] = responses
		if head.RequestState != "" {
			next["requestState"] = head.RequestState
		}
		p = next
	}
}

// result decodes the result of a successful response.
func result[T any](t *testing.T, m rpc.Message) T {
	t.Helper()
	require.Nil(t, m.Error, "unexpected error: %+v", m.Error)
	var out T
	require.NoError(t, json.Unmarshal(m.Result, &out))
	return out
}

// notificationMethods lists the methods of collected notifications.
func (c *testClient) notificationMethods() []string {
	out := make([]string, 0, len(c.notifications))
	for _, n := range c.notifications {
		out = append(out, n.Method)
	}
	return out
}

// listener is an open subscriptions/listen stream.
type listener struct {
	t      *testing.T
	events chan rpc.Message
	final  chan rpc.Message
}

// listen opens subscriptions/listen with the filter.
func (c *testClient) listen(filter map[string]any) *listener {
	c.t.Helper()
	resp := c.post("subscriptions/listen", map[string]any{keyNotifications: filter}, nil)
	require.Equal(c.t, http.StatusOK, resp.StatusCode)
	c.t.Cleanup(func() { _ = resp.Body.Close() })
	l := &listener{t: c.t, events: make(chan rpc.Message, 64), final: make(chan rpc.Message, 1)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), eventPrefix)
			if !ok || data == "" {
				continue
			}
			var m rpc.Message
			if json.Unmarshal([]byte(data), &m) != nil {
				continue
			}
			if m.IsNotification() {
				l.events <- m
			} else {
				l.final <- m
			}
		}
		close(l.events)
	}()
	return l
}

// next waits for the next notification of the stream.
func (l *listener) next() rpc.Message {
	l.t.Helper()
	select {
	case m, ok := <-l.events:
		require.True(l.t, ok, "stream closed")
		return m
	case <-timeAfter():
		require.FailNow(l.t, "timed out waiting for a notification")
		return rpc.Message{}
	}
}

// expectNone asserts that no notification arrives shortly.
func (l *listener) expectNone() {
	l.t.Helper()
	select {
	case m := <-l.events:
		require.FailNow(l.t, "unexpected notification", m.Method)
	case <-shortWait():
	}
}

// srvOf returns the test server of the client.
func (c *testClient) srvOf(t *testing.T) *httptest.Server {
	t.Helper()
	return c.srv
}

func jsonUnmarshal(raw json.RawMessage, dst any) error { return json.Unmarshal(raw, dst) }
