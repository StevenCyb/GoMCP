package mcp_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const subscriptionIDKey = "io.modelcontextprotocol/subscriptionId"

func listenServer(t *testing.T) (*mcp.Server, *testClient) {
	t.Helper()
	s := newBasicServer()
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: "x://watched", Name: "w", Subscribable: true},
		func(mcp.Context) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: "x://plain", Name: "p"},
		func(mcp.Context) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "touch"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.ResourceUpdated("x://watched")
		ctx.ToolsChanged()
		ctx.Progress(1, 2, "half")
		ctx.Log().Error("noise")
		return "ok", nil
	}))
	return s, connect(t, newServer(t, s), nil)
}

func subscriptionID(t *testing.T, m rpc.Message) json.RawMessage {
	t.Helper()
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
	}
	require.NoError(t, json.Unmarshal(m.Params, &p))
	return p.Meta[subscriptionIDKey]
}

func TestListenAcknowledgesFirst(t *testing.T) {
	_, c := listenServer(t)
	l := c.listen(map[string]any{keyToolsListChanged: true, keyResourceSubs: []string{"x://watched", "x://plain", "x://nope"}})

	ack := l.next()
	assert.Equal(t, "notifications/subscriptions/acknowledged", ack.Method)
	assert.JSONEq(t, `1`, string(subscriptionID(t, ack)), "the subscription id is the id of the request")
	assert.JSONEq(t, `{"toolsListChanged":true,"resourceSubscriptions":["x://watched"]}`,
		string(rpc.MustJSON(mustField(t, ack.Params, keyNotifications))), "only what the server honors is acknowledged")
}

func mustField(t *testing.T, raw json.RawMessage, field string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	return m[field]
}

func TestListenDeliversWhatWasAskedFor(t *testing.T) {
	s, c := listenServer(t)
	l := c.listen(map[string]any{keyToolsListChanged: true, keyResourceSubs: []string{"x://watched"}})
	_ = l.next() // acknowledged

	s.ToolsChanged()
	changed := l.next()
	assert.Equal(t, "notifications/tools/list_changed", changed.Method)
	assert.JSONEq(t, `1`, string(subscriptionID(t, changed)), "every notification is tagged")

	s.ResourceUpdated("x://watched")
	updated := l.next()
	assert.Equal(t, "notifications/resources/updated", updated.Method)
	assert.JSONEq(t, `"x://watched"`, string(mustField(t, updated.Params, "uri")))

	// not requested: prompts, resources list, another uri
	s.PromptsChanged()
	s.ResourcesChanged()
	s.ResourceUpdated("x://plain")
	l.expectNone()
}

func TestListenIgnoresRequestScopedNotifications(t *testing.T) {
	_, c := listenServer(t)
	l := c.listen(map[string]any{keyToolsListChanged: true, keyResourceSubs: []string{"x://watched"}})
	_ = l.next()

	worker := connect(t, c.srvOf(t), nil)
	worker.logLevel, worker.progressToken = "debug", "tok"
	_ = callTool(t, worker, "touch", nil)
	assert.Equal(t, []string{"notifications/progress", "notifications/message"}, worker.notificationMethods(), "they go to the request that asked")

	assert.Equal(t, "notifications/resources/updated", l.next().Method)
	assert.Equal(t, "notifications/tools/list_changed", l.next().Method)
	l.expectNone() // no progress and no message on the listen stream
}

func TestListenEndsGracefullyWhenTheServerCloses(t *testing.T) {
	s, c := listenServer(t)
	l := c.listen(map[string]any{keyToolsListChanged: true})
	_ = l.next()

	s.Close()
	select {
	case final := <-l.final:
		assert.Equal(t, "complete", mustString(t, final.Result, "resultType"))
		assert.JSONEq(t, `1`, string(mustField(t, mustField(t, final.Result, "_meta"), subscriptionIDKey)))
	case <-timeAfter():
		t.Fatal("the listen stream was not closed with a response")
	}
}

func mustString(t *testing.T, raw json.RawMessage, field string) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(mustField(t, raw, field), &s))
	return s
}

func TestListenNeedsAnSSEClient(t *testing.T) {
	_, c := listenServer(t)
	res := c.once("subscriptions/listen", map[string]any{keyNotifications: map[string]any{keyToolsListChanged: true}}, map[string]string{"Accept": "application/json"})
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeInvalidRequest, res.Error.Code)
}

func TestListenForTasksNeedsTheExtension(t *testing.T) {
	_, c := listenServer(t)
	resp := c.post("subscriptions/listen", map[string]any{keyNotifications: map[string]any{"taskIds": []string{"abc"}}}, nil)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSeveralListenersGetTheirOwnTag(t *testing.T) {
	s, c := listenServer(t)
	first := c.listen(map[string]any{keyToolsListChanged: true})
	second := c.listen(map[string]any{keyToolsListChanged: true})
	assert.JSONEq(t, `1`, string(subscriptionID(t, first.next())))
	assert.JSONEq(t, `2`, string(subscriptionID(t, second.next())))

	s.ToolsChanged()
	assert.JSONEq(t, `1`, string(subscriptionID(t, first.next())))
	assert.JSONEq(t, `2`, string(subscriptionID(t, second.next())))
}

func TestListenStreamsAreLimited(t *testing.T) {
	s := newBasicServer(mcp.WithAuth(limitScheme()), mcp.WithLimits(mcp.Limits{Streams: 3, StreamsPerCaller: 2}))
	srv := newServer(t, s)
	filter := map[string]any{keyToolsListChanged: true}
	open := func(c *testClient) {
		_ = c.listen(filter).next()
	}
	flooder := connectAs(t, srv, "stream-flooder", nil)
	open(flooder)
	open(flooder)
	res := flooder.once("subscriptions/listen", map[string]any{keyNotifications: filter}, nil)
	require.NotNil(t, res.Error, "the third stream of one caller is refused")
	assert.Equal(t, rpc.CodeInvalidRequest, res.Error.Code)
	assert.Contains(t, res.Error.Message, "caller")

	open(connectAs(t, srv, "stream-other", nil))
	res = connectAs(t, srv, "stream-third", nil).once("subscriptions/listen", map[string]any{keyNotifications: filter}, nil)
	require.NotNil(t, res.Error, "the server wide limit holds")
	assert.Equal(t, rpc.CodeInvalidRequest, res.Error.Code)
}

func TestListenEntriesAreLimited(t *testing.T) {
	s := newBasicServer(mcp.WithLimits(mcp.Limits{ListenEntries: 2}))
	c := connect(t, newServer(t, s), nil)
	res := c.once("subscriptions/listen", map[string]any{keyNotifications: map[string]any{"resourceSubscriptions": []string{"x://one", "x://two", "x://three"}}}, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code)

	_ = c.listen(map[string]any{"resourceSubscriptions": []string{"x://one", "x://two"}}).next()
}
