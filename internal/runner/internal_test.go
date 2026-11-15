package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goModFile = "go.mod"

func TestOptionsWritersDefaultToTheProcessStreams(t *testing.T) {
	assert.Same(t, os.Stdout, Options{}.stdout())
	assert.Same(t, os.Stderr, Options{}.stderr())

	var out, errOut bytes.Buffer
	o := Options{Stdout: &out, Stderr: &errOut}
	assert.Same(t, &out, o.stdout())
	assert.Same(t, &errOut, o.stderr())
}

func TestEnvironment(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFile), []byte("module example.com/e\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("FROM_ENV=1\n"), 0o600))

	t.Run("default .env of the module", func(t *testing.T) {
		env, err := environment(Options{Dir: dir})
		require.NoError(t, err)
		assert.Contains(t, env, "FROM_ENV=1")
		assert.NotContains(t, env, "GOMCP_ADDR=:1")
	})
	t.Run("addr wins as the last entry", func(t *testing.T) {
		env, err := environment(Options{Dir: dir, Addr: "127.0.0.1:1"})
		require.NoError(t, err)
		assert.Equal(t, "GOMCP_ADDR=127.0.0.1:1", env[len(env)-1])
	})
	t.Run("explicit dotenv", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "custom.env")
		require.NoError(t, os.WriteFile(other, []byte("CUSTOM=yes\n"), 0o600))
		env, err := environment(Options{Dir: dir, DotEnv: other})
		require.NoError(t, err)
		assert.Contains(t, env, "CUSTOM=yes")
		assert.NotContains(t, env, "FROM_ENV=1")
	})
	t.Run("no module", func(t *testing.T) {
		_, err := environment(Options{Dir: t.TempDir()})
		require.ErrorIs(t, err, errNoModule)
	})
	t.Run("unparsable dotenv", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.env")
		require.NoError(t, os.WriteFile(bad, []byte("KEY=\"open\n"), 0o600))
		_, err := environment(Options{Dir: dir, DotEnv: bad})
		require.Error(t, err)
	})
}

func TestPrepareWritesOverlayAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFile), []byte("module example.com/p\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte("// @MCP.Server(\"p\")\npackage api\n"), 0o600))

	pr, err := prepare(dir)
	require.NoError(t, err)
	assert.FileExists(t, pr.overlay)
	assert.FileExists(t, filepath.Join(pr.tmp, "main.go"))
	overlay, err := os.ReadFile(pr.overlay)
	require.NoError(t, err)
	var parsed struct{ Replace map[string]string }
	require.NoError(t, json.Unmarshal(overlay, &parsed))
	require.Len(t, parsed.Replace, 1)
	for from, to := range parsed.Replace { // JSON escapes backslashes, so look at the decoded paths
		assert.Equal(t, filepath.Join(dir, ".gomcp", "main.go"), from)
		assert.Equal(t, filepath.Join(pr.tmp, "main.go"), to)
	}
	assert.NoDirExists(t, filepath.Join(dir, ".gomcp"), "nothing is written into the module")

	pr.cleanup()
	assert.NoDirExists(t, pr.tmp)
}

func TestPrepareErrors(t *testing.T) {
	_, err := prepare(t.TempDir())
	require.Error(t, err, "no module")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFile), []byte("module example.com/p\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte("// @MCP.Nope\npackage api\n"), 0o600))
	_, err = prepare(dir)
	require.ErrorContains(t, err, "unknown annotation")
}

func TestSnapshotWatchesOnlySources(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"a.go", "a_test.go", goModFile, "go.sum", ".env", "notes.txt",
		"sub/b.go", "vendor/v.go", ".git/c.go", "_x/d.go", "testdata/e.go",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}
	snap := snapshot(dir)
	got := map[string]bool{}
	for path := range snap {
		rel, err := filepath.Rel(dir, path)
		require.NoError(t, err)
		got[filepath.ToSlash(rel)] = true
	}
	assert.Equal(t, map[string]bool{"a.go": true, goModFile: true, "go.sum": true, ".env": true, "sub/b.go": true}, got)
}

const helperEnv = "GOMCP_RUNNER_TEST_HELPER"

// TestMain lets the test binary act as a tiny server: it waits for an interrupt, or exits right away.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "wait":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		fmt.Println("helper ready")
		<-sig
		os.Exit(0)
	case "fail":
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// lockedBuffer is written by the child process copier and read by the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe
}

func helper(t *testing.T, mode string) (*process, *lockedBuffer) {
	t.Helper()
	out := &lockedBuffer{}
	env := append(os.Environ(), helperEnv+"="+mode)
	p, err := start(t.Context(), testBinary(t), env, Options{Stdout: out, Stderr: out})
	require.NoError(t, err)
	return p, out
}

func TestProcessStopInterruptsTheServer(t *testing.T) {
	p, out := helper(t, "wait")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "helper ready") }, 10*time.Second, 10*time.Millisecond)

	p.stop()
	select {
	case <-p.done:
	default:
		t.Fatal("stop returned before the process exited")
	}
	if runtime.GOOS != "windows" { // Windows cannot send an interrupt, stop kills the process there
		require.NoError(t, p.err, "a graceful shutdown exits with 0")
	}

	p.stop() // stopping an exited process is a no-op
}

func TestProcessExitErrorIsReported(t *testing.T) {
	p, _ := helper(t, "fail")
	<-p.done
	var exitErr *exec.ExitError
	require.ErrorAs(t, p.err, &exitErr)
	assert.Equal(t, 3, exitErr.ExitCode())
	p.stop() // already done
}

func TestStartMissingBinary(t *testing.T) {
	_, err := start(context.Background(), filepath.Join(t.TempDir(), "nope"), nil, Options{})
	require.ErrorContains(t, err, "start server")
}
