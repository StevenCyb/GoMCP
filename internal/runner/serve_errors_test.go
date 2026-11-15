package runner_test

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeWithoutModule(t *testing.T) {
	err := runner.Serve(t.Context(), runner.Options{Dir: t.TempDir(), Stdout: io.Discard, Stderr: io.Discard})
	require.ErrorContains(t, err, "module not found")
}

func TestServeReportsAnnotationErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/bad\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte("// @MCP.Nope\npackage api\n"), 0o600))
	err := runner.Serve(t.Context(), runner.Options{Dir: dir, Stdout: io.Discard, Stderr: io.Discard})
	require.ErrorContains(t, err, "unknown annotation")
}

func TestBuildPassesTags(t *testing.T) {
	dir := demoModule(t)
	// a file only compiled with the tag makes the build fail, proving the tag reaches go build
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tagged.go"), []byte("//go:build gomcptag\n\npackage api\n\nvar _ = undefinedSymbol\n"), 0o600))
	out := filepath.Join(t.TempDir(), "demo")

	require.NoError(t, runner.Build(t.Context(), dir, out, ""))
	require.ErrorContains(t, runner.Build(t.Context(), dir, out, "gomcptag"), "build failed")
}

func TestLoadDotEnvErrors(t *testing.T) {
	_, err := runner.LoadDotEnv(t.TempDir(), nil)
	require.ErrorContains(t, err, "read", "a directory cannot be read as a file")

	bad := filepath.Join(t.TempDir(), "bad.env")
	require.NoError(t, os.WriteFile(bad, []byte("A=\"open\n"), 0o600))
	_, err = runner.LoadDotEnv(bad, nil)
	require.ErrorContains(t, err, "unterminated")
}

func TestParseDotEnvEscapes(t *testing.T) {
	got, err := runner.ParseDotEnv("A=\"a\\tb\\\\c\"\n")
	require.NoError(t, err)
	assert.Equal(t, "a\tb\\c", got["A"])

	_, err = runner.ParseDotEnv("A='open\n")
	require.ErrorContains(t, err, "unterminated")
	_, err = runner.ParseDotEnv("A=\"trailing\\")
	require.ErrorContains(t, err, "unterminated")
}

func TestWatchKeepsRunningWithABrokenDotEnv(t *testing.T) {
	dir := demoModule(t)
	bad := filepath.Join(t.TempDir(), "bad.env")
	require.NoError(t, os.WriteFile(bad, []byte("A=\"open\n"), 0o600))

	logs := &syncBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runner.Watch(ctx, runner.Options{Dir: dir, DotEnv: bad, Stdout: io.Discard, Stderr: io.Discard})
	}()
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "unterminated quote") }, 60*time.Second, 50*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestUnusableTempDir(t *testing.T) {
	dir := demoModule(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} { // TMPDIR on Unix, TMP and TEMP on Windows
		t.Setenv(name, missing)
	}
	o := runner.Options{Dir: dir, Stdout: io.Discard, Stderr: io.Discard}

	require.ErrorContains(t, runner.Serve(t.Context(), o), "create temp dir")
	require.ErrorContains(t, runner.Watch(t.Context(), o), "create temp dir")
	require.ErrorContains(t, runner.Build(t.Context(), dir, filepath.Join(t.TempDir(), "out"), ""), "create temp dir")
}

func TestServeReportsAServerThatExitsWithAnError(t *testing.T) {
	dir := demoModule(t)
	failing := strings.Replace(demoSource, `println("demo setup"); return nil`, `return errSetup`, 1) +
		"\nvar errSetup = errors.New(\"setup failed\")\n"
	failing = strings.Replace(failing, "import \"github.com/StevenCyb/GoMCP/pkg/mcp\"", "import (\n\t\"errors\"\n\n\t\"github.com/StevenCyb/GoMCP/pkg/mcp\"\n)", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(failing), 0o600))

	var logs syncBuffer
	err := runner.Serve(t.Context(), runner.Options{Dir: dir, Addr: freeAddr(t), Stdout: &logs, Stderr: &logs})
	require.ErrorContains(t, err, "server exited")
	assert.Contains(t, logs.String(), "setup failed", "the server's error output is forwarded")
}
