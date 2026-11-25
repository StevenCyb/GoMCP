package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	toolFunc = "func T(ctx mcp.Context, in Empty) (Out, error) { return Out{}, nil }\n"
	pkgAPI   = "package api\n"
	annTool  = "// @MCP.Tool(\"x\")\n"
)

func TestModifierErrors(t *testing.T) {
	tests := []struct {
		name, source, contains string
	}{
		{"duplicate hint", annTool + "// @MCP.Hint(readOnly)\n// @MCP.Hint(idempotent)\n" + toolFunc, "Hint appears more than once"},
		{"duplicate task", annTool + "// @MCP.Task(optional)\n// @MCP.Task(required)\n" + toolFunc, "Task appears more than once"},
		{"modifier of another feature", "// @MCP.Prompt(\"p\")\n// @MCP.Hint(readOnly)\nfunc P(ctx mcp.Context, a A) (mcp.Messages, error) { return nil, nil }\n", "Hint is not valid on a Prompt"},
		{"subscribable on a tool", annTool + "// @MCP.Subscribable\n" + toolFunc, "Subscribable is not valid on a Tool"},
		{"unknown annotation next to feature", annTool + "// @MCP.Bogus\n" + toolFunc, `unknown annotation "Bogus"`},
		{"resource list without template", "// @MCP.ResourceList\nfunc L(ctx mcp.Context, p mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) { return mcp.Page[mcp.ResourceRef]{}, nil }\n", "needs the template"},
		{"resource list shape", "// @MCP.Resource(\"note:///{id}\")\nfunc R(ctx mcp.Context, u URI) (mcp.Contents, error) { return nil, nil }\n" +
			"// @MCP.ResourceList(\"note:///{id}\")\nfunc L(ctx mcp.Context) (mcp.Page[mcp.ResourceRef], error) { return mcp.Page[mcp.ResourceRef]{}, nil }\n", "2 parameters"},
		{"setup too many results", "// @MCP.Setup\nfunc S() (int, error) { return 0, nil }\n", "nothing or an error"},
		{"setup wrong result", "// @MCP.Setup\nfunc S() int { return 0 }\n", "nothing or an error"},
		{"prompt needs a name", "// @MCP.Prompt\nfunc P(ctx mcp.Context, a A) (mcp.Messages, error) { return nil, nil }\n", "Prompt needs a name"},
		{"prompt complete without values", "// @MCP.Prompt(\"p\")\n// @MCP.Complete(arg=\"a\")\nfunc P(ctx mcp.Context, a A) (mcp.Messages, error) { return nil, nil }\n", "values"},
		{"endpoint without slash", "// @MCP.Endpoint(\"mcp\")\npackage api\n", "starting with /"},
		{"endpoint without value", "// @MCP.Endpoint\npackage api\n", "starting with /"},
		{"keepalive without value", "// @MCP.KeepAlive\npackage api\n", "needs a duration"},
		{"instructions without text", "// @MCP.Instructions\npackage api\n", "needs a text"},
		{"allowed origin without value", "// @MCP.AllowedOrigin\npackage api\n", "needs an origin"},
		{"negative page size", "// @MCP.PageSize(-1)\npackage api\n", "PageSize"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.source
			if !strings.HasSuffix(src, pkgAPI) {
				src = header + src
			}
			msg := parseErr(t, map[string]string{fileA: src})
			assert.Contains(t, msg, tt.contains)
			assert.Contains(t, msg, "a.go:")
		})
	}
}

func TestBlockCommentAnnotations(t *testing.T) {
	p := parse(t, map[string]string{fileA: header + "/*\n@MCP.Tool(\"block\", description=\"From a block\")\n*/\n" + toolFunc})
	require.Len(t, p.Tools, 1)
	assert.Equal(t, "block", p.Tools[0].Name)
	assert.Equal(t, "From a block", p.Tools[0].Description)
}

func TestRepeatableModifiersAreAccepted(t *testing.T) {
	p := parse(t, map[string]string{fileA: header + annTool + "// @MCP.Requires(sampling)\n// @MCP.Requires(roots)\n" + toolFunc})
	require.Len(t, p.Tools, 1)
	assert.Equal(t, []string{"sampling", "roots"}, p.Tools[0].Requires)
}

func TestSetupVariants(t *testing.T) {
	p := parse(t, map[string]string{fileA: header + "// @MCP.Setup\nfunc A() error { return nil }\n// @MCP.Setup\nfunc B() {}\n"})
	require.Len(t, p.Setup, 2)
	assert.True(t, p.Setup[0].ReturnsError)
	assert.False(t, p.Setup[1].ReturnsError)
}

func TestPromptCompleteValues(t *testing.T) {
	p := parse(t, map[string]string{fileA: header + "// @MCP.Prompt(\"p\")\n// @MCP.Complete(arg=\"lang\", values=\"go,rust\")\nfunc P(ctx mcp.Context, a A) (mcp.Messages, error) { return nil, nil }\n"})
	require.Len(t, p.Completions, 1)
	assert.Equal(t, "p", p.Completions[0].Prompt)
	assert.Equal(t, "lang", p.Completions[0].Arg)
	assert.Equal(t, []string{"go", "rust"}, p.Completions[0].Values)
}

func TestFindModule(t *testing.T) {
	tests := []struct {
		name, goMod, wantPath, wantErr string
	}{
		{"plain", "module example.com/a\n\ngo 1.26\n", "example.com/a", ""},
		{"quoted", "module \"example.com/q\"\n", "example.com/q", ""},
		{"backquoted", "module `example.com/b`\n", "example.com/b", ""},
		{"trailing comment", "module example.com/c // note\n", "example.com/c", ""},
		{"tab separated", "module\texample.com/t\n", "example.com/t", ""},
		{"skips lookalike directive", "modulefoo bar\nmodule example.com/d\n", "example.com/d", ""},
		{"no module line", "go 1.26\n", "", "no module line"},
		{"empty module line", "module\n", "", "no module line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := module(t, map[string]string{"go.mod": tt.goMod})
			root, path, err := parser.FindModule(dir)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, path)
			want, evalErr := filepath.EvalSymlinks(dir)
			require.NoError(t, evalErr)
			got, evalErr := filepath.EvalSymlinks(root)
			require.NoError(t, evalErr)
			assert.Equal(t, want, got)
		})
	}
}

func TestParseSkipsNestedModules(t *testing.T) {
	p := parse(t, map[string]string{
		fileA:            header + annTool + toolFunc,
		"inner/go.mod":   "module example.com/inner\n",
		"inner/inner.go": header + "// @MCP.Tool(\"inner\")\n" + toolFunc,
	})
	require.Len(t, p.Tools, 1)
	assert.Equal(t, "x", p.Tools[0].Name)
}

func TestParseSyntaxErrorInSource(t *testing.T) {
	msg := parseErr(t, map[string]string{fileA: "package api\nfunc {"})
	assert.Contains(t, msg, fileA)
}

const handlerHeader = "package api\n\nimport \"net/http\"\n\n"

func TestHTTPHandlers(t *testing.T) {
	p := parse(t, map[string]string{fileA: handlerHeader +
		"// @MCP.HTTPHandler(\"POST /elicitation/complete\")\nfunc Complete(w http.ResponseWriter, r *http.Request) {}\n" +
		"// @MCP.HTTPHandler(\"GET /connect\")\nfunc Page(http.ResponseWriter, *http.Request) {}\n" +
		"// @MCP.HTTPHandler(\"POST /completion/{id}\")\nfunc ByID(w http.ResponseWriter, r *http.Request) {}\n"})
	require.Len(t, p.Handlers, 3)
	assert.Equal(t, "POST /completion/{id}", p.Handlers[2].Pattern, "wildcards of http.ServeMux are allowed")
	assert.Equal(t, "POST /elicitation/complete", p.Handlers[0].Pattern)
	assert.Equal(t, "Complete", p.Handlers[0].Name)
	assert.Equal(t, "GET /connect", p.Handlers[1].Pattern)
	assert.Equal(t, "Page", p.Handlers[1].Name)
}

func TestHTTPHandlerErrors(t *testing.T) {
	tests := []struct {
		name, source, contains string
	}{
		{"no pattern", "// @MCP.HTTPHandler\nfunc H(w http.ResponseWriter, r *http.Request) {}\n", "needs a ServeMux pattern"},
		{"empty pattern", "// @MCP.HTTPHandler(\"\")\nfunc H(w http.ResponseWriter, r *http.Request) {}\n", "needs a ServeMux pattern"},
		{"invalid pattern", "// @MCP.HTTPHandler(\"POST\")\nfunc H(w http.ResponseWriter, r *http.Request) {}\n", "is not valid"},
		{"one parameter", "// @MCP.HTTPHandler(\"POST /x\")\nfunc H(w http.ResponseWriter) {}\n", "http.ResponseWriter, *http.Request"},
		{"a result", "// @MCP.HTTPHandler(\"POST /x\")\nfunc H(w http.ResponseWriter, r *http.Request) error { return nil }\n", "without results"},
		{"wrong types", "// @MCP.HTTPHandler(\"POST /x\")\nfunc H(a string, b int) {}\n", "http.ResponseWriter, *http.Request"},
		{"request by value", "// @MCP.HTTPHandler(\"POST /x\")\nfunc H(w http.ResponseWriter, r http.Request) {}\n", "http.ResponseWriter, *http.Request"},
		{"with receiver", "// @MCP.HTTPHandler(\"POST /x\")\nfunc (h T) H(w http.ResponseWriter, r *http.Request) {}\ntype T struct{}\n", "receiver"},
		{"modifier", "// @MCP.HTTPHandler(\"POST /x\")\n// @MCP.Hint(readOnly)\nfunc H(w http.ResponseWriter, r *http.Request) {}\n", "Hint is not valid on a HTTPHandler"},
		{"overlapping wildcards", "// @MCP.HTTPHandler(\"POST /x/{a}\")\nfunc A(w http.ResponseWriter, r *http.Request) {}\n// @MCP.HTTPHandler(\"POST /x/{b}\")\nfunc B(w http.ResponseWriter, r *http.Request) {}\n", "conflicts"},
		{"same pattern twice", "// @MCP.HTTPHandler(\"POST /x\")\nfunc A(w http.ResponseWriter, r *http.Request) {}\n// @MCP.HTTPHandler(\"POST /x\")\nfunc B(w http.ResponseWriter, r *http.Request) {}\n", "conflicts"},
		{"same as the endpoint", "// @MCP.HTTPHandler(\"/mcp\")\nfunc H(w http.ResponseWriter, r *http.Request) {}\n", "conflicts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseErr(t, map[string]string{fileA: handlerHeader + tt.source})
			assert.Contains(t, msg, tt.contains)
			assert.Contains(t, msg, "a.go:")
		})
	}
}
