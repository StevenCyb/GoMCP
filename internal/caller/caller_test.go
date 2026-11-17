package caller_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type textIn struct {
	Text string `json:"text"`
	Tag  string `json:"tag,omitempty"`
}

type confirm struct {
	OK bool `desc:"Really?" json:"ok"`
}

func newServer(t *testing.T) *mcp.Server {
	t.Helper()
	s := mcp.NewServer(mcp.Info{Name: "fake", Version: "1"}, mcp.WithPageSize(2))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "echo", Description: "Echoes the text.\nSecond line."},
		func(_ mcp.Context, in textIn) (string, error) { return "echo " + in.Text, nil }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "fail"},
		func(_ mcp.Context, _ mcp.Empty) (string, error) { return "", mcp.ToolError("it broke") }))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "ask", Requires: []string{mcp.CapElicitation}},
		func(ctx mcp.Context, _ mcp.Empty) (string, error) {
			_, _, err := mcp.Elicit[confirm](ctx, "Delete it?")
			return "done", err
		}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "where", Requires: []string{mcp.CapRoots}},
		func(ctx mcp.Context, _ mcp.Empty) (string, error) {
			roots, err := ctx.Roots()
			if err != nil || len(roots) == 0 {
				return "", err
			}
			return roots[0].URI, nil
		}))
	return s
}

func client(t *testing.T, roots ...string) *caller.Client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := newServer(t)
	go func() {
		_ = s.ServeStdio(context.Background(), inR, outW)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return caller.New(inW, outR, roots)
}

func TestToolsFollowsPagesAndFormats(t *testing.T) {
	tools, err := client(t).Tools()
	require.NoError(t, err)
	require.Len(t, tools, 4, "page size 2 means two pages")

	out := caller.FormatTools(tools)
	assert.Contains(t, out, "echo   Echoes the text.\n")
	assert.NotContains(t, out, "Second line")
	assert.Contains(t, out, "required: text")
	assert.Contains(t, out, "optional: tag")
}

func TestCallText(t *testing.T) {
	raw, err := client(t).Call("echo", json.RawMessage(`{"text":"hi"}`))
	require.NoError(t, err)
	assert.Equal(t, "echo hi", caller.Text(raw))
	assert.Contains(t, caller.Indent(raw), "\n  \"content\"")
}

func TestCallToolError(t *testing.T) {
	raw, err := client(t).Call("fail", nil)
	var toolErr *caller.ToolError
	require.ErrorAs(t, err, &toolErr)
	assert.Equal(t, "it broke", toolErr.Message)
	assert.NotNil(t, raw)
}

func TestCallProtocolError(t *testing.T) {
	_, err := client(t).Call("nope", nil)
	var rpcErr *caller.RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.NotZero(t, rpcErr.Code)
	assert.Contains(t, err.Error(), rpcErr.Message)
}

func TestCallInputRequiredIsNamed(t *testing.T) {
	_, err := client(t).Call("ask", nil)
	var inputErr *caller.InputError
	require.ErrorAs(t, err, &inputErr)
	assert.Contains(t, err.Error(), "elicitation: Delete it?")

	_, err = client(t).Call("where", nil)
	require.ErrorAs(t, err, &inputErr)
	assert.Contains(t, err.Error(), "--root")
}

func TestCallAnswersRoots(t *testing.T) {
	raw, err := client(t, t.TempDir()).Call("where", nil)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(caller.Text(raw), "file:///"), caller.Text(raw))
}

func TestParseArgs(t *testing.T) {
	got, err := caller.ParseArgs(`{"a":1}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, string(got))

	got, err = caller.ParseArgs("")
	require.NoError(t, err)
	assert.Empty(t, got)

	for _, bad := range []string{"{", "[1]", "null", `"x"`, "5"} {
		_, err = caller.ParseArgs(bad)
		require.Error(t, err, bad)
	}
}

func TestClosedServer(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	_ = inR.Close()
	_ = outW.Close()
	_, err := caller.New(inW, outR, nil).Tools()
	require.Error(t, err)
}
