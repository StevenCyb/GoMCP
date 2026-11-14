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

func rootsServer(t *testing.T, opts ...mcp.Option) *mcp.Server {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "where"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		roots, err := ctx.Roots()
		if err != nil {
			return "", err
		}
		return roots[0].URI, nil
	}))
	return s
}

func TestStateKeyOption(t *testing.T) {
	caps := map[string]any{mcp.CapRoots: map[string]any{}}
	c := connect(t, newServer(t, rootsServer(t, mcp.WithStateKey([]byte("0123456789abcdef0123456789abcdef")))), caps)
	c.onRequest = func(rpc.Message) (any, *rpc.Error) {
		return map[string]any{mcp.CapRoots: []any{map[string]any{keyURI: "file:///w"}}}, nil
	}
	res := result[toolResult](t, callTool(t, c, "where", nil))
	assert.Equal(t, "file:///w", res.Content[0][keyText])
}

func TestAllowedOriginsAndKeepAliveOptions(t *testing.T) {
	s := newBasicServer(mcp.WithAllowedOrigins("https://claude.ai"), mcp.WithKeepAlive(20*time.Millisecond))
	srv := newServer(t, s)
	c := connect(t, srv, nil)

	resp := c.post("tools/list", map[string]any{}, map[string]string{"Origin": "https://evil.example"})
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = c.post("tools/list", map[string]any{}, map[string]string{"Origin": "https://claude.ai"})
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestContextReportsListChanges(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "touch"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.ResourcesChanged()
		ctx.PromptsChanged()
		ctx.ToolsChanged()
		return "ok", nil
	}))
	c := connect(t, newServer(t, s), nil)
	l := c.listen(map[string]any{keyToolsListChanged: true, "promptsListChanged": true, "resourcesListChanged": true})
	assert.Equal(t, "notifications/subscriptions/acknowledged", l.next().Method)

	_ = callTool(t, c, "touch", nil)
	got := map[string]bool{}
	for range 3 {
		got[l.next().Method] = true
	}
	assert.Equal(t, map[string]bool{
		"notifications/tools/list_changed":     true,
		"notifications/prompts/list_changed":   true,
		"notifications/resources/list_changed": true,
	}, got)
}
