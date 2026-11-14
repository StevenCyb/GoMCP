package mcp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type discoverResult struct {
	ResultType        string         `json:"resultType"`
	SupportedVersions []string       `json:"supportedVersions"`
	Capabilities      map[string]any `json:"capabilities"`
	Instructions      string         `json:"instructions"`
	TTLMs             int64          `json:"ttlMs"`
	CacheScope        string         `json:"cacheScope"`
	Meta              struct {
		ServerInfo struct {
			Name        string     `json:"name"`
			Title       string     `json:"title"`
			Version     string     `json:"version"`
			Description string     `json:"description"`
			WebsiteURL  string     `json:"websiteUrl"`
			Icons       []mcp.Icon `json:"icons"`
		} `json:"io.modelcontextprotocol/serverInfo"` //nolint:tagliatelle // key defined by the MCP specification
	} `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
}

func newBasicServer(opts ...mcp.Option) *mcp.Server {
	return mcp.NewServer(mcp.Info{Name: "notes", Version: "1.0.0"}, opts...)
}

func discover(t *testing.T, s *mcp.Server) discoverResult {
	t.Helper()
	c := connect(t, newServer(t, s), nil)
	return result[discoverResult](t, c.call("server/discover", nil))
}

func TestDiscover(t *testing.T) {
	s := mcp.NewServer(mcp.Info{
		Name: "notes", Title: "Notes", Version: "1.2.3", Description: "d", WebsiteURL: "https://example.com",
		Icons: []mcp.Icon{{Src: "https://example.com/i.png", MIMEType: mimePNG}},
	}, mcp.WithInstructions("be nice"), mcp.WithCache(time.Minute, mcp.CachePublic))
	res := discover(t, s)

	assert.Equal(t, "complete", res.ResultType)
	assert.Equal(t, []string{protocol}, res.SupportedVersions)
	assert.Equal(t, "be nice", res.Instructions)
	assert.Equal(t, "notes", res.Meta.ServerInfo.Name)
	assert.Equal(t, "Notes", res.Meta.ServerInfo.Title)
	assert.Equal(t, "1.2.3", res.Meta.ServerInfo.Version)
	assert.Equal(t, "d", res.Meta.ServerInfo.Description)
	assert.Equal(t, "https://example.com", res.Meta.ServerInfo.WebsiteURL)
	require.Len(t, res.Meta.ServerInfo.Icons, 1)
	assert.Equal(t, int64(60000), res.TTLMs)
	assert.Equal(t, "public", res.CacheScope)
}

func TestDiscoverDefaultsToNoCaching(t *testing.T) {
	res := discover(t, newBasicServer())
	assert.Equal(t, int64(0), res.TTLMs)
	assert.Equal(t, "private", res.CacheScope)
}

func TestCapabilitiesFollowRegisteredFeatures(t *testing.T) {
	empty := discover(t, newBasicServer())
	assert.NotContains(t, empty.Capabilities, "tools")
	assert.NotContains(t, empty.Capabilities, "resources")
	assert.NotContains(t, empty.Capabilities, "prompts")
	assert.NotContains(t, empty.Capabilities, "extensions")
	assert.Contains(t, empty.Capabilities, "logging")

	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t", Task: mcp.TaskOptional}, func(mcp.Context, mcp.Empty) (mcp.Empty, error) { return mcp.Empty{}, nil }))
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: "x://a", Name: "a", Subscribable: true},
		func(mcp.Context) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "p"}, func(mcp.Context, mcp.Empty) (mcp.Messages, error) { return nil, nil }))
	require.NoError(t, s.AddPromptCompletion("p", "arg", mcp.Values("a")))

	res := discover(t, s)
	assert.Equal(t, map[string]any{"listChanged": true}, res.Capabilities["tools"])
	assert.Equal(t, map[string]any{"listChanged": true, "subscribe": true}, res.Capabilities["resources"])
	assert.Equal(t, map[string]any{"listChanged": true}, res.Capabilities["prompts"])
	assert.Contains(t, res.Capabilities, "completions")
	assert.Equal(t, map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}, res.Capabilities["extensions"])
}

func TestRemovedMethodsAreNotFound(t *testing.T) {
	c := connect(t, newServer(t, newBasicServer()), nil)
	for _, method := range []string{"ping", "logging/setLevel", "resources/subscribe", "resources/unsubscribe", "tasks/result", "tasks/list", "does/not/exist"} {
		resp := c.post(method, nil, nil)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}
}

func TestInitializeIsRefusedWithTheSupportedVersions(t *testing.T) {
	c := connect(t, newServer(t, newBasicServer()), nil)
	res := c.once("initialize", map[string]any{"protocolVersion": "2025-11-25"}, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeUnsupportedProtocol, res.Error.Code)
}

func TestEveryResultCarriesTypeAndServerInfo(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil }))
	c := connect(t, newServer(t, s), nil)

	for method, params := range map[string]any{"tools/list": nil, "tools/call": map[string]any{keyName: "t"}} {
		res := result[map[string]any](t, c.call(method, params))
		assert.Equal(t, "complete", res["resultType"], method)
		meta, _ := res["_meta"].(map[string]any)
		info, _ := meta["io.modelcontextprotocol/serverInfo"].(map[string]any)
		assert.Equal(t, "notes", info[keyName], method)
	}
}

func TestListResultsAreCacheable(t *testing.T) {
	s := newBasicServer(mcp.WithCache(30*time.Second, mcp.CachePublic))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil }))
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: "x://a", Name: "a"}, func(mcp.Context) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("a")}, nil }))
	c := connect(t, newServer(t, s), nil)

	for method, params := range map[string]any{
		"tools/list": nil, "prompts/list": nil, "resources/list": nil, "resources/templates/list": nil,
		"resources/read": map[string]any{keyURI: "x://a"},
	} {
		res := result[map[string]any](t, c.call(method, params))
		assert.InDelta(t, 30000, res["ttlMs"], 0, method)
		assert.Equal(t, "public", res["cacheScope"], method)
	}
	call := result[map[string]any](t, c.call("tools/call", map[string]any{keyName: "t"}))
	assert.NotContains(t, call, "ttlMs", "a tool call is not cacheable")
}

func TestDuplicateRegistrationFails(t *testing.T) {
	s := newBasicServer()
	handler := func(mcp.Context, mcp.Empty) (mcp.Empty, error) { return mcp.Empty{}, nil }
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, handler))
	require.Error(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, handler))
	require.Error(t, mcp.AddTool(s, mcp.ToolDef{}, handler), "name is required")
	require.Error(t, mcp.AddTool(s, mcp.ToolDef{Name: "has space"}, handler), "tool names are limited to A-Z a-z 0-9 _ . -")
	require.Error(t, mcp.AddTool(s, mcp.ToolDef{Name: string(make([]byte, 129))}, handler))
}
