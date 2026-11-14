package e2e_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	keyJSONRPC   = "jsonrpc"
	keyMethod    = "method"
	keyParams    = "params"
	keyName      = "name"
	keyArguments = "arguments"
	jsonRPC      = "2.0"
	specVersion  = "2026-07-28"
	metaVersion  = "io.modelcontextprotocol/protocolVersion"
	metaCaps     = "io.modelcontextprotocol/clientCapabilities"
	keyURI       = "uri"
	keyTaskID    = "taskId"
	keyStatus    = "status"
	keyAction    = "action"
	keyContent   = "content"
	keyResultTyp = "resultType"
	keyURL       = "url"
	toolImport   = "import_notes"
	keyText      = "text"
	valAccept    = "accept"
	resInputReq  = "input_required"
)

type obj = map[string]any

// e2eClient is a small client of the stateless protocol (2026-07-28). Every request carries
// its version and capabilities; input requests are answered through answer and retried.
type e2eClient struct {
	t     *testing.T
	url   string
	token string
	caps  obj
	id    int
	// answer replies to the input requests of the server (elicitation, sampling, roots).
	answer func(method string, params obj) obj
	// notes are the notifications received on response streams.
	notes []obj
	// used records the tools, prompts and resources this client called.
	used *usage
}

// usage is shared between the clients of one test run.
type usage struct{ tools, prompts, resources map[string]bool }

func newUsage() *usage { return &usage{tools: obj2set(), prompts: obj2set(), resources: obj2set()} }

func obj2set() map[string]bool { return map[string]bool{} }

func (c *e2eClient) with(token string, caps obj) *e2eClient {
	return &e2eClient{t: c.t, url: c.url, token: token, caps: caps, used: c.used}
}

// post sends one request with the headers of the protocol.
func (c *e2eClient) post(method string, params obj, headers map[string]string) *http.Response {
	c.t.Helper()
	c.id++
	body, err := json.Marshal(obj{keyJSONRPC: jsonRPC, "id": c.id, keyMethod: method, keyParams: params})
	require.NoError(c.t, err)
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", specVersion)
	req.Header.Set("Mcp-Method", method)
	if name := nameOf(method, params); name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	return resp
}

// raw sends the params as they are, with the given headers changed, and returns result and error.
func (c *e2eClient) raw(method string, params obj, headers map[string]string) (obj, obj) {
	c.t.Helper()
	resp := c.post(method, params, headers)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	return decode(c.t, body)
}

// nameOf is the value of the Mcp-Name header of a request.
func nameOf(method string, params obj) string {
	var key string
	switch method {
	case "tools/call", "prompts/get":
		key = keyName
	case "resources/read":
		key = keyURI
	case "tasks/get", "tasks/update", "tasks/cancel":
		key = keyTaskID
	default:
		return ""
	}
	s, _ := params[key].(string)
	return s
}

// meta returns params with the request metadata.
func (c *e2eClient) meta(params obj) obj {
	out := obj{}
	maps.Copy(out, params)
	m := obj{metaVersion: specVersion, metaCaps: c.caps}
	given, _ := params["_meta"].(obj)
	maps.Copy(m, given)
	out["_meta"] = m
	return out
}

// once sends a request and returns result and error object of the response.
func (c *e2eClient) once(method string, params obj) (obj, obj) {
	c.t.Helper()
	resp := c.post(method, c.meta(params), nil)
	defer func() { _ = resp.Body.Close() }()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, err := io.ReadAll(resp.Body)
		require.NoError(c.t, err)
		return decode(c.t, raw)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || data == "" {
			continue
		}
		var m obj
		require.NoError(c.t, json.Unmarshal([]byte(data), &m))
		if m[keyMethod] != nil {
			c.notes = append(c.notes, m)
			continue
		}
		return split(m)
	}
	require.FailNow(c.t, "stream ended without a response")
	return nil, nil
}

func decode(t *testing.T, raw []byte) (obj, obj) {
	t.Helper()
	var m obj
	require.NoError(t, json.Unmarshal(raw, &m), string(raw))
	return split(m)
}

func split(m obj) (obj, obj) {
	result, _ := m["result"].(obj)
	rpcErr, _ := m["error"].(obj)
	return result, rpcErr
}

// call sends a request; while the server answers input_required it answers the input requests
// with c.answer and retries with the answers and the requestState, like a client does.
func (c *e2eClient) call(method string, params obj) (obj, obj) {
	c.t.Helper()
	for round := 0; ; round++ {
		res, rpcErr := c.once(method, params)
		if rpcErr != nil || res[keyResultTyp] != resInputReq {
			return res, rpcErr
		}
		require.Less(c.t, round, 8, "too many rounds")
		require.NotNil(c.t, c.answer, "unexpected input request: %v", res["inputRequests"])
		params = c.retry(params, res)
	}
}

func (c *e2eClient) retry(params, res obj) obj {
	answers := obj{}
	requests, _ := res["inputRequests"].(obj)
	for key, item := range requests {
		request, _ := item.(obj)
		method, _ := request[keyMethod].(string)
		p, _ := request[keyParams].(obj)
		answers[key] = c.answer(method, p)
	}
	next := obj{}
	maps.Copy(next, params)
	next["inputResponses"] = answers
	if state, ok := res["requestState"].(string); ok {
		next["requestState"] = state
	}
	return next
}

// tool calls a tool and returns its result; protocol errors fail the test.
func (c *e2eClient) tool(name string, args obj) obj {
	c.t.Helper()
	c.used.tools[name] = true
	res, rpcErr := c.call("tools/call", obj{keyName: name, keyArguments: args})
	require.Nil(c.t, rpcErr, "tools/call %s", name)
	return res
}

// toolErr calls a tool and returns the protocol error.
func (c *e2eClient) toolErr(name string, args obj) obj {
	c.t.Helper()
	c.used.tools[name] = true
	res, rpcErr := c.call("tools/call", obj{keyName: name, keyArguments: args})
	require.NotNil(c.t, rpcErr, "tools/call %s should fail, got %v", name, res)
	return rpcErr
}

func (c *e2eClient) prompt(name string, args obj) (obj, obj) {
	c.used.prompts[name] = true
	return c.call("prompts/get", obj{keyName: name, keyArguments: args})
}

func (c *e2eClient) read(uri string) (obj, obj) {
	c.used.resources[uri] = true
	return c.call("resources/read", obj{keyURI: uri})
}

func text(t *testing.T, res obj) string {
	t.Helper()
	content, _ := res[keyContent].([]any)
	require.NotEmpty(t, content, "no content in %v", res)
	first, _ := content[0].(obj)
	s, _ := first[keyText].(string)
	return s
}

func structured(t *testing.T, res obj) obj {
	t.Helper()
	s, ok := res["structuredContent"].(obj)
	require.True(t, ok, "no structuredContent in %v", res)
	return s
}

func isError(res obj) bool {
	b, _ := res["isError"].(bool)
	return b
}

// stream is an open subscriptions/listen stream.
type stream struct {
	t      *testing.T
	events chan obj
}

func (c *e2eClient) listen(notifications obj) *stream {
	c.t.Helper()
	resp := c.post("subscriptions/listen", c.meta(obj{"notifications": notifications}), nil)
	require.Equal(c.t, http.StatusOK, resp.StatusCode)
	c.t.Cleanup(func() { _ = resp.Body.Close() })
	st := &stream{t: c.t, events: make(chan obj, 64)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok && data != "" {
				var m obj
				if json.Unmarshal([]byte(data), &m) == nil {
					st.events <- m
				}
			}
		}
		close(st.events)
	}()
	return st
}

// waitFor returns the first event with that method.
func (s *stream) waitFor(method string) obj {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-s.events:
			require.True(s.t, ok, "stream closed")
			if m[keyMethod] == method {
				return m
			}
		case <-deadline:
			require.FailNow(s.t, "timed out waiting for "+method)
		}
	}
}

// freeAddr returns a local address that is free right now.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}
