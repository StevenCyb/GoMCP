package mcp_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type addIn struct {
	Text string `desc:"Note body" json:"text"          min:"1"`
	Tag  string `enum:"work,idea" json:"tag,omitempty"`
}

type note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

const keyText = "text"

type toolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent"`
	IsError           bool             `json:"isError"`
}

type toolList struct {
	Tools []struct {
		Name         string         `json:"name"`
		Title        string         `json:"title"`
		Description  string         `json:"description"`
		InputSchema  map[string]any `json:"inputSchema"`
		OutputSchema map[string]any `json:"outputSchema"`
		Annotations  map[string]any `json:"annotations"`
		Icons        []mcp.Icon     `json:"icons"`
		Meta         map[string]any `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
	} `json:"tools"`
	NextCursor string `json:"nextCursor"`
}

func toolsServer(t *testing.T, opts ...mcp.Option) (*mcp.Server, *testClient) {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{
		Name: "add_note", Title: "Add note", Description: "Adds a note",
		Icons: []mcp.Icon{{Src: "https://example.com/add.svg"}},
		Hint:  mcp.Hint{Idempotent: true},
		Meta:  map[string]any{keyTeam: valDocs},
	}, func(_ mcp.Context, in addIn) (note, error) {
		return note{ID: idNote1, Text: in.Text}, nil
	}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "echo"},
		func(_ mcp.Context, in addIn) (string, error) { return "echo " + in.Text, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "count"},
		func(mcp.Context, mcp.Empty) (int, error) { return 42, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "nothing"},
		func(mcp.Context, mcp.Empty) (mcp.Empty, error) { return mcp.Empty{}, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "rich"},
		func(mcp.Context, mcp.Empty) (mcp.Result[note], error) {
			return mcp.Result[note]{
				Structured: note{ID: "n", Text: "t"},
				Content:    []mcp.Content{mcp.Text("custom"), mcp.ResourceLink("note:///n")},
			}, nil
		}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "tool_error"},
		func(mcp.Context, mcp.Empty) (string, error) { return "", mcp.ToolError("note not found") }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "plain_error"},
		func(mcp.Context, mcp.Empty) (string, error) { return "", errors.New("disk on fire") }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "protocol_error"},
		func(mcp.Context, mcp.Empty) (string, error) { return "", mcp.ErrInvalidParams("bad thing") }))
	return s, connect(t, newServer(t, s), nil)
}

func callTool(t *testing.T, c *testClient, name string, args any) rpc.Message {
	t.Helper()
	return c.call("tools/call", map[string]any{keyName: name, keyArgs: args})
}

func TestToolsList(t *testing.T) {
	_, c := toolsServer(t)
	list := result[toolList](t, c.call("tools/list", nil))
	require.Len(t, list.Tools, 8)

	add := list.Tools[0]
	assert.Equal(t, "add_note", add.Name)
	assert.Equal(t, "Add note", add.Title)
	assert.Equal(t, "Adds a note", add.Description)
	assert.Equal(t, "object", add.InputSchema["type"])
	assert.Equal(t, []any{keyText}, add.InputSchema["required"])
	props, ok := add.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "text")
	assert.Equal(t, "object", add.OutputSchema["type"], "struct results get an output schema")
	assert.Equal(t, map[string]any{"idempotentHint": true}, add.Annotations, "only set hints are sent")
	require.Len(t, add.Icons, 1)
	assert.Equal(t, map[string]any{keyTeam: valDocs}, add.Meta)

	byName := map[string]int{}
	for i, tool := range list.Tools {
		byName[tool.Name] = i
	}
	assert.Nil(t, list.Tools[byName["echo"]].OutputSchema, "string results have no output schema")
	assert.Nil(t, list.Tools[byName["count"]].OutputSchema)
	assert.Nil(t, list.Tools[byName["nothing"]].OutputSchema)
	assert.Equal(t, map[string]any{keyType: "object", "additionalProperties": false}, list.Tools[byName["nothing"]].InputSchema,
		"a tool without parameters says so")
}

func TestToolsListPagination(t *testing.T) {
	_, c := toolsServer(t, mcp.WithPageSize(3))

	var names []string
	cursor := ""
	pages := 0
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		list := result[toolList](t, c.call("tools/list", params))
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		pages++
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, names, 8)
	assert.Equal(t, "add_note", names[0], "registration order is kept")
}

func TestToolCallStructuredResult(t *testing.T) {
	_, c := toolsServer(t)
	res := result[toolResult](t, callTool(t, c, "add_note", map[string]any{keyText: "hello"}))
	assert.False(t, res.IsError)
	assert.Equal(t, map[string]any{"id": idNote1, keyText: "hello"}, res.StructuredContent)
	require.Len(t, res.Content, 1)
	assert.Equal(t, "text", res.Content[0]["type"])
	assert.JSONEq(t, `{"id":"note-1","text":"hello"}`, res.Content[0][keyText].(string))
}

func TestToolCallPlainResults(t *testing.T) {
	_, c := toolsServer(t)

	str := result[toolResult](t, callTool(t, c, "echo", map[string]any{keyText: "x"}))
	assert.Nil(t, str.StructuredContent)
	assert.Equal(t, "echo x", str.Content[0]["text"])

	n := result[toolResult](t, callTool(t, c, "count", nil))
	assert.Equal(t, "42", n.Content[0]["text"])

	empty := result[toolResult](t, callTool(t, c, "nothing", nil))
	assert.Empty(t, empty.Content)
	assert.Nil(t, empty.StructuredContent)
	assert.False(t, empty.IsError)
}

func TestToolCallRichResult(t *testing.T) {
	_, c := toolsServer(t)
	res := result[toolResult](t, callTool(t, c, "rich", nil))
	assert.Equal(t, map[string]any{"id": "n", keyText: "t"}, res.StructuredContent)
	require.Len(t, res.Content, 2, "custom content replaces the generated text block")
	assert.Equal(t, "custom", res.Content[0][keyText])
	assert.Equal(t, "resource_link", res.Content[1]["type"])
	assert.Equal(t, "note:///n", res.Content[1]["uri"])
}

func TestToolCallErrors(t *testing.T) {
	_, c := toolsServer(t)

	toolErr := result[toolResult](t, callTool(t, c, "tool_error", nil))
	assert.True(t, toolErr.IsError)
	assert.Equal(t, "note not found", toolErr.Content[0]["text"])

	plain := result[toolResult](t, callTool(t, c, "plain_error", nil))
	assert.True(t, plain.IsError, "plain errors are execution errors the model can see")
	assert.Equal(t, "disk on fire", plain.Content[0]["text"])

	proto := callTool(t, c, "protocol_error", nil)
	require.NotNil(t, proto.Error)
	assert.Equal(t, rpc.CodeInvalidParams, proto.Error.Code)

	unknown := callTool(t, c, "nope", nil)
	require.NotNil(t, unknown.Error)
	assert.Equal(t, rpc.CodeInvalidParams, unknown.Error.Code)

	bad := c.call("tools/call", map[string]any{})
	require.NotNil(t, bad.Error)
	assert.Equal(t, rpc.CodeInvalidParams, bad.Error.Code)
}

func TestToolCallValidatesArguments(t *testing.T) {
	_, c := toolsServer(t)

	cases := map[string]any{
		"missing required": map[string]any{},
		"wrong type":       map[string]any{keyText: 12},
		"below minimum":    map[string]any{keyText: ""},
		"bad enum":         map[string]any{keyText: "x", "tag": "other"},
		"not an object":    "text",
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			res := result[toolResult](t, callTool(t, c, "add_note", args))
			assert.True(t, res.IsError, "validation errors let the model correct itself")
		})
	}
}

func TestToolCallRequiresClientCapability(t *testing.T) {
	s := newBasicServer()
	called := false
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "needs_sampling", Requires: []string{mcp.CapSampling, mcp.CapElicitationURL}},
		func(mcp.Context, mcp.Empty) (string, error) {
			called = true
			return "ok", nil
		}))
	srv := newServer(t, s)

	without := connect(t, srv, nil)
	resp := without.post("tools/call", map[string]any{keyName: "needs_sampling"}, nil)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	res := without.once("tools/call", map[string]any{keyName: "needs_sampling"}, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeMissingCapability, res.Error.Code)
	assert.JSONEq(t, `{"requiredCapabilities":{"sampling":{},"elicitation":{"url":{}}}}`, string(rpc.MustJSON(res.Error.Data)))
	assert.False(t, called)

	with := connect(t, srv, map[string]any{mcp.CapSampling: map[string]any{}, mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}})
	assert.Equal(t, "ok", firstText(t, callTool(t, with, "needs_sampling", nil)))
	assert.True(t, called)
}

func TestStructuredResults(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "list"}, func(mcp.Context, mcp.Empty) ([]note, error) { return nil, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "counts"}, func(mcp.Context, mcp.Empty) (map[string]int, error) { return map[string]int{"a": 1}, nil }))
	c := connect(t, newServer(t, s), nil)

	list := result[toolList](t, c.call("tools/list", nil))
	assert.Equal(t, "array", list.Tools[0].OutputSchema[keyType], "an output schema can be any schema, not only an object")
	assert.Equal(t, "object", list.Tools[1].OutputSchema[keyType])

	res := result[map[string]any](t, callTool(t, c, "list", nil))
	assert.Equal(t, []any{}, res["structuredContent"], "an empty list is [] not null")
	res = result[map[string]any](t, callTool(t, c, "counts", nil))
	assert.Equal(t, map[string]any{"a": float64(1)}, res["structuredContent"])
}

type headerIn struct {
	Region string `header:"Region"  json:"region"`
	Count  int    `header:"X-Count" json:"count,omitempty"`
	Query  string `json:"query"`
}

func TestParamHeaders(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "sql"}, func(_ mcp.Context, in headerIn) (string, error) { return in.Region, nil }))
	c := connect(t, newServer(t, s), nil)

	list := result[toolList](t, c.call("tools/list", nil))
	props, _ := list.Tools[0].InputSchema["properties"].(map[string]any)
	region, _ := props["region"].(map[string]any)
	assert.Equal(t, "Region", region["x-mcp-header"])

	call := func(headers map[string]string, args map[string]any) int {
		resp := c.post("tools/call", map[string]any{keyName: "sql", keyArgs: args}, headers)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	args := map[string]any{"region": "eu", "query": "q", "count": 3}
	assert.Equal(t, http.StatusOK, call(map[string]string{headerRegion: "eu", headerCount: "3"}, args))
	assert.Equal(t, http.StatusOK, call(map[string]string{headerRegion: "eu", headerCount: "3.0"}, args), "integers compare by value")
	assert.Equal(t, http.StatusOK, call(map[string]string{headerRegion: "=?base64?ZXU=?=", headerCount: "3"}, args), "base64 encoded values are decoded")
	assert.Equal(t, http.StatusBadRequest, call(map[string]string{headerRegion: "us", headerCount: "3"}, args), "different value")
	assert.Equal(t, http.StatusBadRequest, call(map[string]string{headerCount: "3"}, args), "header missing")
	assert.Equal(t, http.StatusBadRequest, call(map[string]string{headerRegion: "=?base64?!!?=", headerCount: "3"}, args), "bad encoding")

	absent := map[string]any{"region": "eu", "query": "q"}
	assert.Equal(t, http.StatusOK, call(map[string]string{headerRegion: "eu"}, absent), "an absent argument has no header")
	assert.Equal(t, http.StatusBadRequest, call(map[string]string{headerRegion: "eu", headerCount: "3"}, absent), "a header without argument")
}

func TestMiddlewareWrapsToolCalls(t *testing.T) {
	s := newBasicServer()
	var order []string
	s.Use(func(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
		order = append(order, "global:"+string(req.Kind)+":"+req.Name)
		return next(ctx, req)
	})
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{
		Name: "t",
		Use: []mcp.Middleware{func(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
			order = append(order, "tool")
			return next(ctx, req)
		}},
	}, func(mcp.Context, mcp.Empty) (string, error) {
		order = append(order, "handler")
		return "ok", nil
	}))
	c := connect(t, newServer(t, s), nil)

	_ = result[toolResult](t, callTool(t, c, "t", nil))
	assert.Equal(t, []string{"global:tool:t", "tool", "handler"}, order)
}

func TestMiddlewareCanShortCircuit(t *testing.T) {
	s := newBasicServer()
	s.Use(func(mcp.Context, mcp.Request, mcp.Handler) (any, error) {
		return nil, mcp.ErrInvalidParams("blocked")
	})
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"},
		func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil }))
	c := connect(t, newServer(t, s), nil)

	res := callTool(t, c, "t", nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, "blocked", res.Error.Message)
}
