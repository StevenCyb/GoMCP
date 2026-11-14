package mcp_test

import (
	"testing"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func iconServer(t *testing.T, opts ...mcp.Option) (*testClient, string) {
	t.Helper()
	icons := []mcp.Icon{
		{Src: "assets/add.svg"},
		{Src: "/assets/logo.png"},
		{Src: "https://cdn.example.com/x.png"},
		{Src: "data:image/png;base64,AAAA"},
		{Src: "assets/custom.bin", MIMEType: "image/x-custom"},
		{Src: "assets/noext"},
	}
	s := mcp.NewServer(mcp.Info{Name: "n", Version: "1", Icons: icons}, opts...)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t", Icons: icons},
		func(mcp.Context, mcp.Empty) (string, error) { return "", nil }))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "p", Icons: icons},
		func(mcp.Context, mcp.Empty) (mcp.Messages, error) { return nil, nil }))
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate, Name: "n", Icons: icons},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: indexURI, Name: "i", Icons: icons},
		func(mcp.Context) (mcp.Contents, error) { return nil, nil }))
	srv := newServer(t, s)
	return connect(t, srv, nil), srv.URL
}

func assertIcons(t *testing.T, got []mcp.Icon, base string) {
	t.Helper()
	require.Len(t, got, 6)
	assert.Equal(t, mcp.Icon{Src: base + "/assets/add.svg", MIMEType: "image/svg+xml"}, got[0])
	assert.Equal(t, mcp.Icon{Src: base + "/assets/logo.png", MIMEType: mimePNG}, got[1], "a leading slash is fine")
	assert.Equal(t, mcp.Icon{Src: "https://cdn.example.com/x.png", MIMEType: mimePNG}, got[2], "absolute URLs stay, the MIME type is inferred")
	assert.Equal(t, mcp.Icon{Src: "data:image/png;base64,AAAA"}, got[3], "data URIs are left alone")
	assert.Equal(t, mcp.Icon{Src: base + "/assets/custom.bin", MIMEType: "image/x-custom"}, got[4], "an explicit MIME type wins")
	assert.Equal(t, mcp.Icon{Src: base + "/assets/noext"}, got[5], "unknown extensions get no MIME type")
}

func TestIconsResolveAgainstTheBaseURL(t *testing.T) {
	c, _ := iconServer(t, mcp.WithBaseURL("https://notes.example.com/"))
	base := "https://notes.example.com"

	assertIcons(t, result[toolList](t, c.call("tools/list", nil)).Tools[0].Icons, base)
	assertIcons(t, result[promptList](t, c.call("prompts/list", nil)).Prompts[0].Icons, base)
	assertIcons(t, result[resourceList](t, c.call("resources/templates/list", nil)).ResourceTemplates[0].Icons, base)
	assertIcons(t, result[resourceList](t, c.call("resources/list", nil)).Resources[0].Icons, base)
}

func TestIconsResolveAgainstTheRequestHost(t *testing.T) {
	c, url := iconServer(t)
	assertIcons(t, result[toolList](t, c.call("tools/list", nil)).Tools[0].Icons, url)
}

func TestServerInfoIconsAreResolved(t *testing.T) {
	s := mcp.NewServer(mcp.Info{Name: "n", Version: "1", Icons: []mcp.Icon{{Src: "assets/n.png"}}}, mcp.WithBaseURL("https://x.example"))
	res := discover(t, s)
	assert.Equal(t, []mcp.Icon{{Src: "https://x.example/assets/n.png", MIMEType: mimePNG}}, res.Meta.ServerInfo.Icons)
}
