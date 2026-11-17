package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/generator"
	"github.com/StevenCyb/GoMCP/internal/parser"
	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSubstitutesNameAndModule(t *testing.T) {
	files, err := Render("my-server", "git.example.com/me/my-server")
	require.NoError(t, err)
	assert.Contains(t, files["go.mod"], "module git.example.com/me/my-server\n")
	assert.Contains(t, files["go.mod"], "go "+GoVersion)
	assert.Contains(t, files["api.go"], `@MCP.Server("my-server"`)
	assert.Contains(t, files["README.md"], "gomcp stdio .")
	assert.Contains(t, files["README.md"], "bin/my-server")
	assert.Equal(t, ".env\n", files[".gitignore"])
}

func TestRenderRejectsInvalidInput(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a b", "-x"} {
		_, err := Render(name, "example.com/x")
		require.Error(t, err, name)
	}
	for _, module := range []string{"", "a b"} {
		_, err := Render("ok", module)
		require.Error(t, err, module)
	}
}

func TestGoVersionMatchesRepo(t *testing.T) {
	mod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	require.NoError(t, err)
	m := regexp.MustCompile(`(?m)^go (\S+)\s*$`).FindSubmatch(mod)
	require.NotNil(t, m)
	assert.Equal(t, GoVersion, string(m[1]))
}

func TestInitWritesProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newproj")
	var out bytes.Buffer
	require.NoError(t, Init(t.Context(), Options{Dir: dir, NoGet: true, Stdout: &out}))
	for _, f := range []string{"go.mod", "api.go", ".gitignore", "README.md"} {
		assert.FileExists(t, filepath.Join(dir, f))
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(mod), "module example.com/newproj\n")
	assert.Contains(t, out.String(), "gomcp serve .")
}

func TestInitCustomModule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	require.NoError(t, Init(t.Context(), Options{Dir: dir, Module: "foo.io/bar", NoGet: true}))
	mod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	assert.Contains(t, string(mod), "module foo.io/bar\n")
}

func TestInitAcceptsEmptyDirRefusesNonEmpty(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	require.NoError(t, os.Mkdir(empty, 0o750))
	require.NoError(t, Init(t.Context(), Options{Dir: empty, NoGet: true}))

	busy := filepath.Join(root, "busy")
	require.NoError(t, os.Mkdir(busy, 0o750))
	keep := filepath.Join(busy, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("x"), 0o600))
	err := Init(t.Context(), Options{Dir: busy, NoGet: true})
	require.ErrorContains(t, err, "not empty")
	assert.NoFileExists(t, filepath.Join(busy, "go.mod"))
}

func TestInitGetFailureKeepsFilesAndSaysWhatToRun(t *testing.T) {
	t.Setenv("PATH", "") // no go binary
	dir := filepath.Join(t.TempDir(), "p")
	err := Init(t.Context(), Options{Dir: dir, Version: "v1.2.3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "files are written")
	assert.Contains(t, err.Error(), "go get github.com/StevenCyb/GoMCP@v1.2.3")
	assert.FileExists(t, filepath.Join(dir, "api.go"))
}

func TestDependencyVersion(t *testing.T) {
	assert.Equal(t, "github.com/StevenCyb/GoMCP@latest", dependency("dev"))
	assert.Equal(t, "github.com/StevenCyb/GoMCP@latest", dependency(""))
	assert.Equal(t, "github.com/StevenCyb/GoMCP@v0.3.0", dependency("v0.3.0"))
}

// The scaffold must parse, generate and expose the hello tool against the local GoMCP checkout.
func TestScaffoldIsValid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dir := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, Init(t.Context(), Options{Dir: dir, NoGet: true}))

	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	mod = append(mod, "\nrequire github.com/StevenCyb/GoMCP v0.0.0\n\nreplace github.com/StevenCyb/GoMCP => "+
		filepath.ToSlash(root)+"\n"...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o600))

	p, err := parser.Parse(dir)
	require.NoError(t, err)
	require.Len(t, p.Tools, 1)
	assert.Equal(t, "hello", p.Tools[0].Name)
	src, err := generator.Generate(p)
	require.NoError(t, err)
	assert.Contains(t, string(src), "hello")

	// and it compiles
	require.NoError(t, runner.Build(t.Context(), dir, filepath.Join(t.TempDir(), "bin", "proj"+exe), ""))
}
