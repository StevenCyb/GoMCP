package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	version       = "2026-07-28"
	metaV         = "io.modelcontextprotocol/protocolVersion"
	metaC         = "io.modelcontextprotocol/clientCapabilities"
	acceptAll     = "application/json, text/event-stream"
	keyMeta       = "_meta"
	headerVersion = "Mcp-Protocol-Version"
	headerName    = "Mcp-Name"
)

// fakeDispatcher handles a few test methods.
type fakeDispatcher struct {
	started   chan struct{}
	cancelled chan struct{}
}

func newFake() *fakeDispatcher {
	return &fakeDispatcher{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1)}
}

func (d *fakeDispatcher) request(c *transport.Call, req transport.Request) (any, *rpc.Error) {
	switch req.Method {
	case "echo", "tools/call", "prompts/get", "resources/read", "tasks/get":
		return map[string]any{"method": req.Method}, nil
	case "fail":
		return nil, rpc.InvalidParams("nope")
	case "capability":
		return nil, rpc.MissingCapability(map[string]any{"roots": map[string]any{}})
	case "stream":
		c.Notify("notifications/progress", map[string]int{"n": 1})
		c.Notify("notifications/progress", map[string]int{"n": 2})
		return map[string]string{"done": "yes"}, nil
	case "slow":
		d.started <- struct{}{}
		<-c.Context().Done()
		d.cancelled <- struct{}{}
		return nil, rpc.Internal("cancelled")
	case "meta":
		return map[string]any{
			"version": c.Meta().ProtocolVersion, "caps": c.Meta().Capabilities, "logLevel": c.Meta().LogLevel,
			"progress": c.Meta().ProgressToken, "id": c.ID(), "principal": c.Principal(),
		}, nil
	case "wait-close":
		c.Notify("notifications/ready", nil)
		<-c.Closing()
		return map[string]string{"closed": "yes"}, nil
	default:
		return nil, rpc.MethodNotFound(req.Method)
	}
}

func newServer(t *testing.T, cfg transport.Config) (*httptest.Server, *transport.Server, *fakeDispatcher) {
	t.Helper()
	d := newFake()
	ts := transport.New(cfg, d.request)
	srv := httptest.NewServer(ts)
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, d
}

// reply is a fully read HTTP response.
type reply struct {
	Status int
	Header http.Header
	Body   string
}

func (r reply) message(t *testing.T) rpc.Message {
	t.Helper()
	var m rpc.Message
	require.NoError(t, json.Unmarshal([]byte(r.Body), &m), r.Body)
	return m
}

func (r reply) events(t *testing.T) []rpc.Message {
	t.Helper()
	var out []rpc.Message
	for line := range strings.SplitSeq(r.Body, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok && data != "" {
			var m rpc.Message
			require.NoError(t, json.Unmarshal([]byte(data), &m))
			out = append(out, m)
		}
	}
	return out
}

// envelope builds the JSON-RPC request of a client with the required _meta.
func envelope(method string, params map[string]any) string {
	p := map[string]any{}
	maps.Copy(p, params)
	if _, ok := p[keyMeta]; !ok {
		p[keyMeta] = map[string]any{metaV: version, metaC: map[string]any{}}
	}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": p})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// stdHeaders are the headers a correct client sends for method.
func stdHeaders(method string, extra map[string]string) map[string]string {
	h := map[string]string{headerVersion: version, "Mcp-Method": method, "Accept": acceptAll}
	maps.Copy(h, extra)
	return h
}

func do(t *testing.T, method, url, body string, headers map[string]string) reply {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

// call sends a correct request.
func call(t *testing.T, url, method string, params map[string]any) reply {
	t.Helper()
	return do(t, http.MethodPost, url, envelope(method, params), stdHeaders(method, nil))
}

func TestOriginCheck(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{AllowedOrigins: []string{"https://claude.ai"}})
	h := stdHeaders("echo", map[string]string{"Origin": "https://evil.example"})
	assert.Equal(t, http.StatusForbidden, do(t, http.MethodPost, srv.URL, envelope("echo", nil), h).Status)
	h["Origin"] = "https://claude.ai"
	assert.Equal(t, http.StatusOK, do(t, http.MethodPost, srv.URL, envelope("echo", nil), h).Status)
	delete(h, "Origin")
	assert.Equal(t, http.StatusOK, do(t, http.MethodPost, srv.URL, envelope("echo", nil), h).Status, "no Origin header is fine")
}

func TestOriginDefaultsToLocalhost(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	for origin, want := range map[string]int{
		"http://localhost:3000": http.StatusOK, "https://evil.example": http.StatusForbidden, "://bad": http.StatusForbidden,
	} {
		h := stdHeaders("echo", map[string]string{"Origin": origin})
		assert.Equal(t, want, do(t, http.MethodPost, srv.URL, envelope("echo", nil), h).Status, origin)
	}
}

func TestOnlyPostIsAllowed(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		r := do(t, method, srv.URL, "", nil)
		assert.Equal(t, http.StatusMethodNotAllowed, r.Status, method)
		assert.Equal(t, "POST", r.Header.Get("Allow"))
	}
}

func TestNoSessionHeaderIsNeeded(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	r := do(t, http.MethodPost, srv.URL, envelope("echo", nil), stdHeaders("echo", map[string]string{"Mcp-Session-Id": "ignored", "Last-Event-ID": "g-1"}))
	assert.Equal(t, http.StatusOK, r.Status)
	assert.Empty(t, r.Header.Get("Mcp-Session-Id"))
	assert.JSONEq(t, `{"method":"echo"}`, string(r.message(t).Result))
}

func TestMalformedBodies(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	h := stdHeaders("echo", nil)

	r := do(t, http.MethodPost, srv.URL, `[{"jsonrpc":"2.0","id":1,"method":"echo"}]`, h)
	assert.Equal(t, http.StatusBadRequest, r.Status)
	assert.Equal(t, rpc.CodeInvalidRequest, r.message(t).Error.Code, "batching is not supported")

	r = do(t, http.MethodPost, srv.URL, `{not json`, h)
	assert.Equal(t, http.StatusBadRequest, r.Status)
	assert.Equal(t, rpc.CodeParse, r.message(t).Error.Code)

	r = do(t, http.MethodPost, srv.URL, `{"jsonrpc":"2.0","id":1,"result":{}}`, h)
	assert.Equal(t, http.StatusBadRequest, r.Status, "clients do not send responses")
}

func TestLargeBodyIsRejected(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	body := `{"jsonrpc":"2.0","id":1,"method":"echo","params":"` + strings.Repeat("a", 5<<20) + `"}`
	assert.Equal(t, http.StatusRequestEntityTooLarge, do(t, http.MethodPost, srv.URL, body, stdHeaders("echo", nil)).Status)
}

func TestNotificationsAreAccepted(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	r := do(t, http.MethodPost, srv.URL, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, stdHeaders("notifications/cancelled", nil))
	assert.Equal(t, http.StatusAccepted, r.Status)
	assert.Empty(t, r.Body)
}

func TestInitializeIsAnsweredWithTheSupportedVersions(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{}}}`
	r := do(t, http.MethodPost, srv.URL, body, map[string]string{"Accept": acceptAll})
	assert.Equal(t, http.StatusBadRequest, r.Status)
	e := r.message(t).Error
	require.NotNil(t, e)
	assert.Equal(t, rpc.CodeUnsupportedProtocol, e.Code)
	assert.JSONEq(t, `{"supported":["2026-07-28"],"requested":"2025-11-25"}`, string(rpc.MustJSON(e.Data)))
}

func TestMetaIsRequired(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	h := stdHeaders("echo", nil)
	cases := map[string]string{
		"no params":          `{"jsonrpc":"2.0","id":1,"method":"echo"}`,
		"no meta":            `{"jsonrpc":"2.0","id":1,"method":"echo","params":{}}`,
		"no version":         `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"_meta":{"` + metaC + `":{}}}}`,
		"no caps":            `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"_meta":{"` + metaV + `":"` + version + `"}}}`,
		"caps not an object": `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"_meta":{"` + metaV + `":"` + version + `","` + metaC + `":"x"}}}`,
	}
	for name, body := range cases {
		r := do(t, http.MethodPost, srv.URL, body, h)
		assert.Equal(t, http.StatusBadRequest, r.Status, name)
		assert.Equal(t, rpc.CodeInvalidParams, r.message(t).Error.Code, name)
	}
}

func TestUnsupportedVersion(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	meta := map[string]any{metaV: "1900-01-01", metaC: map[string]any{}}
	h := stdHeaders("echo", map[string]string{headerVersion: "1900-01-01"})
	r := do(t, http.MethodPost, srv.URL, envelope("echo", map[string]any{keyMeta: meta}), h)
	assert.Equal(t, http.StatusBadRequest, r.Status)
	e := r.message(t).Error
	assert.Equal(t, rpc.CodeUnsupportedProtocol, e.Code)
	assert.JSONEq(t, `{"supported":["2026-07-28"],"requested":"1900-01-01"}`, string(rpc.MustJSON(e.Data)))
}

func TestStandardHeaders(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	body := envelope("echo", nil)

	for name, h := range map[string]map[string]string{
		"version missing": {"Mcp-Method": "echo", "Accept": acceptAll},
		"version differs": {headerVersion: "2025-11-25", "Mcp-Method": "echo"},
		"method missing":  {headerVersion: version},
		"method differs":  {headerVersion: version, "Mcp-Method": "tools/list"},
	} {
		r := do(t, http.MethodPost, srv.URL, body, h)
		assert.Equal(t, http.StatusBadRequest, r.Status, name)
		assert.Equal(t, rpc.CodeHeaderMismatch, r.message(t).Error.Code, name)
	}
}

func TestMcpNameHeader(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	cases := []struct {
		method string
		params map[string]any
		name   string
	}{
		{"tools/call", map[string]any{"name": "add_note"}, "add_note"},
		{"prompts/get", map[string]any{"name": "triage"}, "triage"},
		{"resources/read", map[string]any{"uri": "ticket://T-1"}, "ticket://T-1"},
		{"tasks/get", map[string]any{"taskId": "abc"}, "abc"},
	}
	for _, tc := range cases {
		body := envelope(tc.method, tc.params)
		ok := do(t, http.MethodPost, srv.URL, body, stdHeaders(tc.method, map[string]string{headerName: tc.name}))
		assert.Equal(t, http.StatusOK, ok.Status, tc.method)

		missing := do(t, http.MethodPost, srv.URL, body, stdHeaders(tc.method, nil))
		assert.Equal(t, http.StatusBadRequest, missing.Status, tc.method+" without Mcp-Name")
		assert.Equal(t, rpc.CodeHeaderMismatch, missing.message(t).Error.Code)

		wrong := do(t, http.MethodPost, srv.URL, body, stdHeaders(tc.method, map[string]string{headerName: "other"}))
		assert.Equal(t, http.StatusBadRequest, wrong.Status, tc.method+" with another name")
	}
}

func TestMcpNameCanBeBase64Encoded(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	body := envelope("tools/call", map[string]any{"name": "grüße"})
	encoded := "=?base64?Z3LDvMOfZQ==?="
	assert.Equal(t, http.StatusOK, do(t, http.MethodPost, srv.URL, body, stdHeaders("tools/call", map[string]string{headerName: encoded})).Status)
	broken := "=?base64?not base64?="
	assert.Equal(t, http.StatusBadRequest, do(t, http.MethodPost, srv.URL, body, stdHeaders("tools/call", map[string]string{headerName: broken})).Status)
	assert.Equal(t, http.StatusBadRequest, do(t, http.MethodPost, srv.URL, body, stdHeaders("tools/call", map[string]string{headerName: "=?base64?Z3LDvMOfZQ=="})).Status)
}

func TestCheckHeadersHook(t *testing.T) {
	cfg := transport.Config{CheckHeaders: func(h http.Header, req transport.Request) *rpc.Error {
		if req.Method == "echo" && h.Get("Mcp-Param-Region") == "" {
			return rpc.HeaderMismatch("Mcp-Param-Region is missing")
		}
		return nil
	}}
	srv, _, _ := newServer(t, cfg)
	r := call(t, srv.URL, "echo", nil)
	assert.Equal(t, http.StatusBadRequest, r.Status)
	assert.Equal(t, rpc.CodeHeaderMismatch, r.message(t).Error.Code)
	assert.Equal(t, http.StatusOK, do(t, http.MethodPost, srv.URL, envelope("echo", nil), stdHeaders("echo", map[string]string{"Mcp-Param-Region": "eu"})).Status)
}

func TestDispatcherErrorsMapToStatus(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	for method, want := range map[string]int{
		"fail": http.StatusOK, "unknown/method": http.StatusNotFound, "capability": http.StatusBadRequest,
	} {
		r := call(t, srv.URL, method, nil)
		assert.Equal(t, want, r.Status, method)
		assert.NotNil(t, r.message(t).Error, method)
	}
	assert.Equal(t, rpc.CodeMethodNotFound, call(t, srv.URL, "unknown/method", nil).message(t).Error.Code)
}

func TestRequestDetailsReachTheDispatcher(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	meta := map[string]any{metaV: version, metaC: map[string]any{"roots": map[string]any{}}, "io.modelcontextprotocol/logLevel": "info", "progressToken": "tok"}
	r := call(t, srv.URL, "meta", map[string]any{keyMeta: meta})
	assert.Equal(t, http.StatusOK, r.Status)
	assert.JSONEq(t, `{"version":"2026-07-28","caps":{"roots":{}},"logLevel":"info","progress":"tok","id":1,"principal":null}`, string(r.message(t).Result))
}

func TestPlainJSONResponse(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	r := call(t, srv.URL, "echo", nil)
	assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"method":"echo"}`, string(r.message(t).Result))
}

func TestStreamingResponse(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	r := call(t, srv.URL, "stream", nil)
	require.Equal(t, http.StatusOK, r.Status)
	assert.Equal(t, "text/event-stream", r.Header.Get("Content-Type"))
	assert.Equal(t, "no", r.Header.Get("X-Accel-Buffering"))

	events := r.events(t)
	require.Len(t, events, 3)
	assert.Equal(t, "notifications/progress", events[0].Method)
	assert.Equal(t, "notifications/progress", events[1].Method)
	assert.JSONEq(t, `{"done":"yes"}`, string(events[2].Result))
	assert.JSONEq(t, `1`, string(events[2].ID))
}

func TestNoSSEWithoutAccept(t *testing.T) {
	srv, _, _ := newServer(t, transport.Config{})
	r := do(t, http.MethodPost, srv.URL, envelope("stream", nil), stdHeaders("stream", map[string]string{"Accept": "application/json"}))
	assert.Equal(t, "application/json", r.Header.Get("Content-Type"), "no upgrade if the client does not accept SSE")
	assert.JSONEq(t, `{"done":"yes"}`, string(r.message(t).Result))
}

func TestKeepAliveComments(t *testing.T) {
	srv, _, d := newServer(t, transport.Config{KeepAlive: 20 * time.Millisecond})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(envelope("wait-close", nil)))
	require.NoError(t, err)
	for k, v := range stdHeaders("wait-close", nil) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_ = d

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case l := <-lines:
			if strings.HasPrefix(l, ":") {
				return
			}
		case <-deadline:
			t.Fatal("no keep-alive comment")
		}
	}
}

func TestClosingEndsLongCalls(t *testing.T) {
	srv, ts, _ := newServer(t, transport.Config{})
	done := make(chan reply, 1)
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(envelope("wait-close", nil)))
		if err != nil {
			return
		}
		for k, v := range stdHeaders("wait-close", nil) {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		done <- reply{Status: resp.StatusCode, Body: string(b)}
	}()
	time.Sleep(100 * time.Millisecond)
	ts.Close()
	select {
	case r := <-done:
		assert.Contains(t, r.Body, `"closed":"yes"`)
	case <-time.After(2 * time.Second):
		t.Fatal("the call did not end on Close")
	}
}

func TestClosingTheConnectionCancelsTheCall(t *testing.T) {
	srv, _, d := newServer(t, transport.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(envelope("slow", nil)))
		if err != nil {
			return
		}
		for k, v := range stdHeaders("slow", nil) {
			req.Header.Set(k, v)
		}
		if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
			_ = resp.Body.Close()
		}
	}()
	<-d.started
	cancel()
	select {
	case <-d.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the call context was not cancelled")
	}
}

func authConfig() transport.Config {
	return transport.Config{
		Authenticate: func(r *http.Request) (any, *transport.AuthFailure) {
			switch r.Header.Get("Authorization") {
			case "":
				return nil, &transport.AuthFailure{Status: http.StatusUnauthorized, Challenge: `Bearer resource_metadata="https://x/meta"`}
			case "Bearer secret":
				return "user-1", nil
			default:
				return nil, &transport.AuthFailure{Status: http.StatusUnauthorized, Challenge: `Bearer error="invalid_token"`}
			}
		},
		Authorize: func(c *transport.Call, method string, _ json.RawMessage) *transport.AuthFailure {
			if method == "fail" && c.Principal() == "user-1" {
				return &transport.AuthFailure{Status: http.StatusForbidden, Challenge: `Bearer error="insufficient_scope"`}
			}
			return nil
		},
	}
}

func TestAuthentication(t *testing.T) {
	srv, _, _ := newServer(t, authConfig())

	r := call(t, srv.URL, "echo", nil)
	assert.Equal(t, http.StatusUnauthorized, r.Status)
	assert.Equal(t, `Bearer resource_metadata="https://x/meta"`, r.Header.Get("WWW-Authenticate"))

	h := stdHeaders("echo", map[string]string{"Authorization": "Bearer wrong"})
	r = do(t, http.MethodPost, srv.URL, envelope("echo", nil), h)
	assert.Equal(t, `Bearer error="invalid_token"`, r.Header.Get("WWW-Authenticate"))

	h["Authorization"] = "Bearer secret"
	assert.Equal(t, http.StatusOK, do(t, http.MethodPost, srv.URL, envelope("echo", nil), h).Status)

	h = stdHeaders("meta", map[string]string{"Authorization": "Bearer secret"})
	assert.Contains(t, do(t, http.MethodPost, srv.URL, envelope("meta", nil), h).Body, `"principal":"user-1"`)
}

func TestAuthorization(t *testing.T) {
	srv, _, _ := newServer(t, authConfig())
	h := stdHeaders("fail", map[string]string{"Authorization": "Bearer secret"})
	r := do(t, http.MethodPost, srv.URL, envelope("fail", nil), h)
	assert.Equal(t, http.StatusForbidden, r.Status)
	assert.Equal(t, `Bearer error="insufficient_scope"`, r.Header.Get("WWW-Authenticate"))
}

func TestSupportedVersions(t *testing.T) {
	assert.Equal(t, []string{"2026-07-28"}, transport.SupportedVersions())
}
