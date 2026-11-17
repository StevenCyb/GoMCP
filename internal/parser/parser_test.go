package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/parser"
	"github.com/StevenCyb/GoMCP/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fileA = "a.go"
	goMod = "module example.com/app\n\ngo 1.26\n"
)

// module writes a Go module with the given files (path -> content) and returns its directory.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = goMod
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return dir
}

func parse(t *testing.T, files map[string]string) *project.Project {
	t.Helper()
	p, err := parser.Parse(module(t, files))
	require.NoError(t, err)
	return p
}

func parseErr(t *testing.T, files map[string]string) string {
	t.Helper()
	_, err := parser.Parse(module(t, files))
	require.Error(t, err)
	return err.Error()
}

const header = "package api\n\nimport (\n\t\"context\"\n\t\"github.com/StevenCyb/GoMCP/pkg/mcp\"\n)\n\nvar _ context.Context\n\n"

func TestParseExampleDirectory(t *testing.T) {
	p, err := parser.Parse("../../examples/features")
	require.NoError(t, err)

	assert.Equal(t, "github.com/StevenCyb/example_mcp", p.Module.Path)
	assert.Equal(t, "notes", p.Server.Name)
	assert.Equal(t, "Notes MCP Example", p.Server.Title)
	assert.Equal(t, "1.0.0", p.Server.Version)
	assert.Equal(t, "https://example.com/notes", p.Server.Website)
	assert.Equal(t, "Use the notes tools to create and read short text notes.", p.Server.Instructions)
	require.Len(t, p.Server.Icons, 1)
	assert.Equal(t, "assets/notes.svg", p.Server.Icons[0].Src)

	assert.Equal(t, "/mcp", p.Transport.Endpoint)
	assert.Equal(t, []string{"https://claude.ai"}, p.Transport.AllowedOrigins)
	assert.Equal(t, "30s", p.Transport.KeepAlive)
	assert.Equal(t, 50, p.Transport.PageSize)
	assert.Equal(t, "5m", p.Transport.CacheTTL)
	assert.Equal(t, "private", p.Transport.CacheScope)

	require.Len(t, p.Middleware, 1)
	assert.Equal(t, "Logging", p.Middleware[0].Name)
	assert.Equal(t, "github.com/StevenCyb/example_mcp", p.Middleware[0].ImportPath)
	require.Len(t, p.Setup, 1)
	require.Len(t, p.Teardown, 1)

	require.Len(t, p.Auth, 2)
	assert.Equal(t, "oauth", p.Auth[0].Type)
	assert.Equal(t, "VerifyToken", p.Auth[0].Verify.Name)
	assert.Equal(t, "https://notes.example.com/mcp", p.Auth[0].Resource)
	assert.Equal(t, []string{"https://auth.example.com"}, p.Auth[0].AuthorizationServers)
	assert.Equal(t, []project.ScopeDef{
		{Name: "notes:read", Description: "Read notes", Default: true},
		{Name: "notes:write", Description: "Create and delete notes"},
	}, p.Auth[0].Scopes)
	assert.Equal(t, "bearer", p.Auth[1].Type)
	assert.Equal(t, "VerifyAPIKey", p.Auth[1].Verify.Name)

	require.Len(t, p.Assets, 1)
	assert.Equal(t, "/assets", p.Assets[0].Prefix)
	assert.Equal(t, "Assets", p.Assets[0].Var.Name)

	names := map[string]project.Tool{}
	for _, tool := range p.Tools {
		names[tool.Name] = tool
	}
	assert.Len(t, names, 7)
	add := names["add_note"]
	assert.Equal(t, "Add", add.Func.Name)
	assert.Equal(t, "Add note", add.Title)
	assert.Equal(t, "Adds a short text note and returns it", add.Description)
	assert.Equal(t, []string{"notes:write"}, add.Scopes)
	assert.Equal(t, []project.Icon{{Src: "assets/add.svg"}}, add.Icons)
	assert.Equal(t, []project.KV{{Key: "team", Value: "docs"}}, add.Meta)
	assert.True(t, names["get_note"].Hint.ReadOnly)
	assert.True(t, names["get_note"].Hint.Idempotent)
	assert.True(t, names["delete_note"].Hint.Destructive)
	assert.Equal(t, []string{"elicitation"}, names["delete_note"].Requires)
	assert.Equal(t, []string{"elicitation:url"}, names["connect_account"].Requires)
	assert.Equal(t, []string{"sampling"}, names["summarize_all"].Requires)
	assert.Equal(t, []string{"roots"}, names["import_notes"].Requires)
	assert.Equal(t, project.Task{Mode: "optional", TTL: "10m", Poll: "2s"}, names["import_notes"].Task)

	require.Len(t, p.Resources, 2)
	var tmpl, static project.Resource
	for _, r := range p.Resources {
		if r.Template {
			tmpl = r
		} else {
			static = r
		}
	}
	assert.Equal(t, "note:///{id}", tmpl.URI)
	assert.Equal(t, "Note", tmpl.Title)
	assert.Equal(t, "text/plain", tmpl.MIME)
	assert.True(t, tmpl.Subscribable)
	require.NotNil(t, tmpl.Annotations)
	assert.Equal(t, []string{"user", "assistant"}, tmpl.Annotations.Audience)
	require.NotNil(t, tmpl.Annotations.Priority)
	assert.InDelta(t, 0.8, *tmpl.Annotations.Priority, 1e-9)
	assert.Equal(t, "note:///index", static.URI)
	assert.Equal(t, "application/json", static.MIME)

	require.Len(t, p.Lists, 1)
	assert.Equal(t, "note:///{id}", p.Lists[0].Template)
	assert.Equal(t, "List", p.Lists[0].Func.Name)

	require.Len(t, p.Prompts, 1)
	assert.Equal(t, "summarize_notes", p.Prompts[0].Name)
	assert.Equal(t, "SummarizeNotes", p.Prompts[0].Func.Name)

	require.Len(t, p.Completions, 3)
	var static3, dynTemplate, dynPrompt *project.Completion
	for i := range p.Completions {
		c := &p.Completions[i]
		switch {
		case c.Func == nil:
			static3 = c
		case c.Template != "":
			dynTemplate = c
		default:
			dynPrompt = c
		}
	}
	require.NotNil(t, static3)
	assert.Equal(t, "summarize_notes", static3.Prompt)
	assert.Equal(t, "style", static3.Arg)
	assert.Equal(t, []string{"short", "bullets"}, static3.Values)
	require.NotNil(t, dynTemplate)
	assert.Equal(t, "note:///{id}", dynTemplate.Template)
	assert.Equal(t, "CompleteID", dynTemplate.Func.Name)
	require.NotNil(t, dynPrompt)
	assert.Equal(t, "summarize_notes", dynPrompt.Prompt)
	assert.Equal(t, "id", dynPrompt.Arg)
}

func TestParsePackageLevelAnnotations(t *testing.T) {
	p := parse(t, map[string]string{fileA: `// @MCP.Server("srv", title="Srv", version="2.0.0", description="d", website="https://w", baseURL="https://base")
// @MCP.Icon("assets/s.png", mime="image/png", sizes="48x48,96x96", theme="dark")
// @MCP.Instructions("hello")
// @MCP.Endpoint("/rpc")
// @MCP.AllowedOrigin("https://a")
// @MCP.AllowedOrigin("https://b")
// @MCP.KeepAlive("5s")
// @MCP.PageSize(10)
// @MCP.Cache("1m")
package api
`})
	assert.Equal(t, project.Server{
		Name: "srv", Title: "Srv", Version: "2.0.0", Description: "d", Website: "https://w", BaseURL: "https://base",
		Instructions: "hello",
		Icons:        []project.Icon{{Src: "assets/s.png", MIME: "image/png", Sizes: []string{"48x48", "96x96"}, Theme: "dark"}},
	}, p.Server)
	assert.Equal(t, project.Transport{
		Endpoint: "/rpc", AllowedOrigins: []string{"https://a", "https://b"}, KeepAlive: "5s", PageSize: 10, CacheTTL: "1m", CacheScope: "private",
	}, p.Transport)
}

func TestParseDefaults(t *testing.T) {
	p := parse(t, map[string]string{fileA: "package api\n"})
	assert.Equal(t, "app", p.Server.Name, "last element of the module path")
	assert.Equal(t, "0.0.0", p.Server.Version)
	assert.Equal(t, "/mcp", p.Transport.Endpoint)
	assert.Empty(t, p.Transport.CacheTTL)
	assert.Empty(t, p.Tools)
}

func TestParseRemovedAnnotationsAreUnknown(t *testing.T) {
	for _, ann := range []string{"Sessions(stateless)", "EventStore(memory, size=5)", "LogLevel(\"info\")"} {
		assert.Contains(t, parseErr(t, map[string]string{fileA: "// @MCP." + ann + "\npackage api\n"}), "unknown annotation", ann)
	}
}

func TestToolFeatures(t *testing.T) {
	p := parse(t, map[string]string{"tools.go": header + `// Adds a note.
// Returns it.
//
// @MCP.Tool("add", title="Add")
// @MCP.Hint(readOnly, destructive, idempotent, openWorld)
// @MCP.Task(required, ttl="1m", poll="1s")
// @MCP.Requires(sampling, roots, elicitation)
// @MCP.Requires(elicitation="url")
// @MCP.AuthScopes("a", "b")
// @MCP.Icon("i.svg")
// @MCP.Icon("j.png", mime="image/png")
// @MCP.Meta(team="docs", level=3, beta=true)
// @MCP.Use(Mw)
func Add(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }

func Mw(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) { return next(ctx, req) }
`})
	require.Len(t, p.Tools, 1)
	tool := p.Tools[0]
	assert.Equal(t, "add", tool.Name)
	assert.Equal(t, "Adds a note. Returns it.", tool.Description, "doc comment is the default description")
	assert.Equal(t, project.Hint{ReadOnly: true, Destructive: true, Idempotent: true, OpenWorld: true}, tool.Hint)
	assert.Equal(t, project.Task{Mode: "required", TTL: "1m", Poll: "1s"}, tool.Task)
	assert.Equal(t, []string{"sampling", "roots", "elicitation", "elicitation:url"}, tool.Requires)
	assert.Equal(t, []string{"a", "b"}, tool.Scopes)
	assert.Len(t, tool.Icons, 2)
	assert.Equal(t, []project.KV{{Key: "beta", Value: true}, {Key: "level", Value: 3.0}, {Key: "team", Value: "docs"}}, tool.Meta)
	require.Len(t, tool.Use, 1)
	assert.Equal(t, "Mw", tool.Use[0].Name)
	assert.Equal(t, "example.com/app", tool.Use[0].ImportPath)
	assert.Equal(t, "api", tool.Use[0].Pkg)
	assert.Equal(t, "tools.go", tool.Func.Pos.File)
	assert.Equal(t, 23, tool.Func.Pos.Line, "position of the function")
}

func TestDescriptionArgumentWins(t *testing.T) {
	p := parse(t, map[string]string{"tools.go": header + `// Some doc.
//
// @MCP.Tool("add", description="From annotation")
func Add(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }
`})
	assert.Equal(t, "From annotation", p.Tools[0].Description)
}

func TestResourceFeatures(t *testing.T) {
	p := parse(t, map[string]string{"res.go": header + `// @MCP.Resource("note:///{id}", title="Note", mime="text/plain", description="d")
// @MCP.Subscribable
// @MCP.Annotations(audience="user", priority=0.5, lastModified="2026-01-01T00:00:00Z")
func Read(ctx mcp.Context, u URI) (mcp.Contents, error) { return nil, nil }

// @MCP.Resource("note:///index")
func Index(ctx mcp.Context) (mcp.Contents, error) { return nil, nil }

// @MCP.ResourceList("note:///{id}")
func List(ctx mcp.Context, page mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) { return mcp.Page[mcp.ResourceRef]{}, nil }
`})
	require.Len(t, p.Resources, 2)
	tmpl, static := p.Resources[0], p.Resources[1]
	assert.True(t, tmpl.Template)
	assert.False(t, static.Template)
	assert.True(t, tmpl.Subscribable)
	assert.Equal(t, "d", tmpl.Description)
	assert.Equal(t, "2026-01-01T00:00:00Z", tmpl.Annotations.LastModified)
	assert.Equal(t, "Read", tmpl.Name, "name defaults to the function name")
	require.Len(t, p.Lists, 1)
}

func TestPromptAndCompletion(t *testing.T) {
	p := parse(t, map[string]string{"p.go": header + `// @MCP.Prompt("sum", title="Sum")
// @MCP.Complete(arg="style", values="a,b")
func Sum(ctx mcp.Context, a Args) (mcp.Messages, error) { return nil, nil }

// @MCP.Complete(for="sum", arg="id")
// @MCP.Complete(for="note:///{id}", arg="id")
func Complete(ctx mcp.Context, partial string, resolved map[string]string) (mcp.Completion, error) { return mcp.Completion{}, nil }

// @MCP.Resource("note:///{id}")
func Read(ctx mcp.Context, u URI) (mcp.Contents, error) { return nil, nil }
`})
	require.Len(t, p.Prompts, 1)
	require.Len(t, p.Completions, 3)
	assert.Equal(t, project.Completion{Prompt: "sum", Arg: "style", Values: []string{"a", "b"}, Pos: p.Completions[0].Pos}, p.Completions[0])
	assert.Equal(t, "sum", p.Completions[1].Prompt)
	assert.Equal(t, "note:///{id}", p.Completions[2].Template)
}

func TestLifecycleMiddlewareAndAssets(t *testing.T) {
	p := parse(t, map[string]string{"x.go": `// @MCP.Use(Log)
package api

import (
	"embed"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// @MCP.Middleware
func Log(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) { return next(ctx, req) }

// @MCP.Setup
func Setup() error { return nil }

// @MCP.Setup
func Setup2() {}

// @MCP.Teardown
func Teardown() {}

// @MCP.ServeAssets("/assets")
//
//go:embed assets
var Assets embed.FS
`})
	require.Len(t, p.Middleware, 1)
	assert.Equal(t, "Log", p.Middleware[0].Name)
	require.Len(t, p.Setup, 2)
	assert.Equal(t, "Setup", p.Setup[0].Name)
	require.Len(t, p.Teardown, 1)
	require.Len(t, p.Assets, 1)
	assert.Equal(t, project.Assets{Prefix: "/assets", Var: p.Assets[0].Var}, p.Assets[0])
	assert.Equal(t, "Assets", p.Assets[0].Var.Name)
}

func TestAuthAnnotations(t *testing.T) {
	p := parse(t, map[string]string{"auth.go": header + `// @MCP.Auth(type="oauth", resource="https://r")
// @MCP.AuthServer("https://as1")
// @MCP.AuthServer("https://as2")
// @MCP.AuthScope("read", description="Read", default)
// @MCP.AuthScope("write")
func Verify(ctx context.Context, token string) (mcp.Principal, error) { return mcp.Principal{}, nil }

// @MCP.Auth(type="bearer")
func VerifyKey(ctx context.Context, token string) (mcp.Principal, error) { return mcp.Principal{}, nil }
`})
	require.Len(t, p.Auth, 2)
	assert.Equal(t, []string{"https://as1", "https://as2"}, p.Auth[0].AuthorizationServers)
	assert.Equal(t, []project.ScopeDef{{Name: "read", Description: "Read", Default: true}, {Name: "write"}}, p.Auth[0].Scopes)
	assert.Equal(t, "bearer", p.Auth[1].Type)
	assert.Empty(t, p.Auth[1].Scopes)
}

func TestMultiplePackagesAndUseAcrossThem(t *testing.T) {
	p := parse(t, map[string]string{
		"a.go": "// @MCP.Use(mw.Logging)\npackage api\n",
		"mw/mw.go": `package mw

import "github.com/StevenCyb/GoMCP/pkg/mcp"

func Logging(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) { return next(ctx, req) }
`,
		"tools/tools.go": header + `// @MCP.Tool("t")
func T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }
`,
	})
	require.Len(t, p.Middleware, 1)
	assert.Equal(t, "example.com/app/mw", p.Middleware[0].ImportPath)
	assert.Equal(t, "mw", p.Middleware[0].Pkg)
	require.Len(t, p.Tools, 1)
	assert.Equal(t, "example.com/app/tools", p.Tools[0].Func.ImportPath)
}

func TestSkippedFilesAndDirectories(t *testing.T) {
	tool := header + "// @MCP.Tool(\"t\")\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n"
	p := parse(t, map[string]string{
		"a_test.go":         tool,
		"vendor/v/v.go":     tool,
		"testdata/td/td.go": tool,
		".hidden/h.go":      tool,
		"_ignored/i.go":     tool,
		"cmd/main.go":       "package main\n\n// @MCP.Tool(\"t\")\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\nfunc main() {}\n",
		"real/real.go":      header + "// @MCP.Tool(\"real\")\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n",
	})
	require.Len(t, p.Tools, 1)
	assert.Equal(t, "real", p.Tools[0].Name)
}

func TestModuleIsFoundFromSubdirectory(t *testing.T) {
	dir := module(t, map[string]string{"sub/a.go": "package sub\n"})
	p, err := parser.Parse(filepath.Join(dir, "sub"))
	require.NoError(t, err)
	assert.Equal(t, "example.com/app", p.Module.Path)
	assert.Equal(t, dir, p.Module.Dir)
}

func TestNoModule(t *testing.T) {
	_, err := parser.Parse(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go.mod")
}

func TestQuotedModulePath(t *testing.T) {
	p, err := parser.Parse(module(t, map[string]string{"go.mod": "module \"example.com/quoted\" // c\n\ngo 1.26\n"}))
	require.NoError(t, err)
	assert.Equal(t, "example.com/quoted", p.Module.Path)
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		contains string
	}{
		{"syntax error", "// @MCP.Tool(\"x)\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "a.go:10"},
		{"unknown annotation", "// @MCP.Nope\nfunc T() {}\n", `unknown annotation "Nope"`},
		{"tool on a type", "// @MCP.Tool(\"x\")\ntype X struct{}\n", "must be placed on a function"},
		{"two feature annotations", "// @MCP.Tool(\"x\")\n// @MCP.Prompt(\"y\")\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "more than one"},
		{"modifier without feature", "// @MCP.Hint(readOnly)\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "Hint"},
		{"tool needs a name", "// @MCP.Tool\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "name"},
		{"tool with receiver", "// @MCP.Tool(\"x\")\nfunc (r R) T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "receiver"},
		{"tool params", "// @MCP.Tool(\"x\")\nfunc T(ctx mcp.Context) (Out, error) { return Out{}, nil }\n", "2 parameters"},
		{"tool results", "// @MCP.Tool(\"x\")\nfunc T(ctx mcp.Context, in Empty) Out { return Out{} }\n", "2 results"},
		{"tool last result", "// @MCP.Tool(\"x\")\nfunc T(ctx mcp.Context, in Empty) (Out, Out) { return Out{}, Out{} }\n", "error"},
		{"tool first param", "// @MCP.Tool(\"x\")\nfunc T(ctx string, in Empty) (Out, error) { return Out{}, nil }\n", "mcp.Context"},
		{"static resource params", "// @MCP.Resource(\"note:///index\")\nfunc R(ctx mcp.Context, u URI) (mcp.Contents, error) { return nil, nil }\n", "1 parameter"},
		{"template resource params", "// @MCP.Resource(\"note:///{id}\")\nfunc R(ctx mcp.Context) (mcp.Contents, error) { return nil, nil }\n", "2 parameters"},
		{"bad hint", "// @MCP.Tool(\"x\")\n// @MCP.Hint(fast)\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "fast"},
		{"bad task mode", "// @MCP.Tool(\"x\")\n// @MCP.Task(sometimes)\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "sometimes"},
		{"bad requires", "// @MCP.Tool(\"x\")\n// @MCP.Requires(telepathy)\nfunc T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n", "telepathy"},
		{"bad auth type", "// @MCP.Auth(type=\"magic\")\nfunc V(ctx context.Context, t string) (mcp.Principal, error) { return mcp.Principal{}, nil }\n", "magic"},
		{"oauth needs resource", "// @MCP.Auth(type=\"oauth\")\nfunc V(ctx context.Context, t string) (mcp.Principal, error) { return mcp.Principal{}, nil }\n", "resource"},
		{"auth scope without auth", "// @MCP.AuthScope(\"x\")\nfunc V(ctx context.Context, t string) (mcp.Principal, error) { return mcp.Principal{}, nil }\n", "AuthScope"},
		{"verifier shape", "// @MCP.Auth(type=\"bearer\")\nfunc V(t string) (mcp.Principal, error) { return mcp.Principal{}, nil }\n", "2 parameters"},
		{"list for unknown template", "// @MCP.ResourceList(\"note:///{id}\")\nfunc L(ctx mcp.Context, p mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) { return mcp.Page[mcp.ResourceRef]{}, nil }\n", "no resource template"},
		{"complete for unknown prompt", "// @MCP.Complete(for=\"nope\", arg=\"a\")\nfunc C(ctx mcp.Context, p string, r map[string]string) (mcp.Completion, error) { return mcp.Completion{}, nil }\n", "unknown prompt"},
		{"complete needs arg", "// @MCP.Prompt(\"p\")\n// @MCP.Complete(values=\"a\")\nfunc P(ctx mcp.Context, a A) (mcp.Messages, error) { return nil, nil }\n", "arg"},
		{"unknown middleware", "// @MCP.Use(Missing)\npackage api\n", "Missing"},
		{"middleware shape", "// @MCP.Use(Bad)\npackage api\n\nfunc Bad(ctx mcp.Context) {}\n", "3 parameters"},
		{"setup shape", "// @MCP.Setup\nfunc S(x int) {}\n", "no parameters"},
		{"two servers", "// @MCP.Server(\"a\")\n// @MCP.Server(\"b\")\npackage api\n", "more than one"},
		{"bad page size", "// @MCP.PageSize(\"x\")\npackage api\n", "PageSize"},
		{"bad duration", "// @MCP.KeepAlive(\"soon\")\npackage api\n", "duration"},
		{"bad cache scope", "// @MCP.Cache(\"1m\", scope=\"world\")\npackage api\n", "world"},
		{"bad cache ttl", "// @MCP.Cache(\"soon\")\npackage api\n", "duration"},
		{"assets on func", "// @MCP.ServeAssets(\"/a\")\nfunc F() {}\n", "variable"},
		{"assets wrong type", "// @MCP.ServeAssets(\"/a\")\nvar A string\n", "embed.FS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.source
			if !strings.Contains(src, "package api") {
				src = header + src
			}
			msg := parseErr(t, map[string]string{fileA: src})
			assert.Contains(t, msg, tt.contains)
			assert.Contains(t, msg, "a.go:", "errors name the file")
		})
	}
}

func TestDuplicateNames(t *testing.T) {
	msg := parseErr(t, map[string]string{fileA: header + "// @MCP.Tool(\"same\")\n" + "func A(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n" +
		"// @MCP.Tool(\"same\")\n" + "func B(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n"})
	assert.Contains(t, msg, "already")
}

func TestAllErrorsAreReportedTogether(t *testing.T) {
	msg := parseErr(t, map[string]string{
		"a.go": header + "// @MCP.Nope\nfunc A() {}\n",
		"b.go": header + "// @MCP.Tool(\"x\")\nfunc B(ctx mcp.Context) {}\n",
	})
	assert.Contains(t, msg, "a.go")
	assert.Contains(t, msg, "b.go")
}
