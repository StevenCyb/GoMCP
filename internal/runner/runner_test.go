package runner_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoModule = "github.com/StevenCyb/GoMCP"

// repoRoot finds the root of this repository.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if data, readErr := os.ReadFile(filepath.Join(dir, "go.mod")); readErr == nil && strings.Contains(strings.ReplaceAll(string(data), "\r\n", "\n"), "module "+repoModule+"\n") {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "repository root not found")
		dir = parent
	}
}

const demoSource = `// @MCP.Server("demo", version="1.0.0")
// @MCP.Instructions("Say hello.")
package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type HelloIn struct {
	Name string ` + "`json:\"name\"`" + `
}

// @MCP.Tool("hello", description="Greets someone")
func Hello(ctx mcp.Context, in HelloIn) (string, error) { return "Hello " + in.Name, nil }

// @MCP.Setup
func Setup() error { println("demo setup"); return nil }
`

// demoModule writes a small annotated module that uses this repository through a replace directive.
func demoModule(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a Go program")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	goMod := fmt.Sprintf("module example.com/demo\n\ngo 1.26.2\n\nrequire %s v0.0.0\n\nreplace %s => %s\n", repoModule, repoModule, root)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600))
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(demoSource), 0o600))
	return dir
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

type rpcClient struct {
	t   *testing.T
	url string
	id  int
}

const (
	keyJSONRPC   = "jsonrpc"
	keyMethod    = "method"
	keyParams    = "params"
	keyName      = "name"
	keyArguments = "arguments"
	jsonRPC      = "2.0"
	specVersion  = "2026-07-28"
	metaVersion  = "io.modelcontextprotocol/protocolVersion"
	metaCaps     = "io.modelcontextprotocol/clientCapabilities"
)

// withMeta adds what every request of the stateless protocol carries.
func withMeta(params map[string]any, caps map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, params)
	meta, _ := out["_meta"].(map[string]any)
	merged := map[string]any{metaVersion: specVersion, metaCaps: caps}
	maps.Copy(merged, meta)
	out["_meta"] = merged
	return out
}

func (c *rpcClient) call(method string, params map[string]any) map[string]any {
	c.t.Helper()
	c.id++
	body, err := json.Marshal(map[string]any{keyJSONRPC: jsonRPC, "id": c.id, keyMethod: method, keyParams: withMeta(params, map[string]any{})})
	require.NoError(c.t, err)
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", specVersion)
	req.Header.Set("Mcp-Method", method)
	if name, _ := params[keyName].(string); name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(bytes.NewReader(raw))
		var last string
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok && data != "" {
				last = data
			}
		}
		raw = []byte(last)
	}
	var out map[string]any
	require.NoError(c.t, json.Unmarshal(raw, &out), string(raw))
	return out
}

// waitFor retries until the server answers.
func waitFor(t *testing.T, url string) *rpcClient {
	t.Helper()
	c := &rpcClient{t: t, url: url}
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader("{}"))
		if err != nil {
			return false
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}, 20*time.Second, 100*time.Millisecond, "server did not start")
	require.NotNil(t, c.call("tools/list", map[string]any{})["result"])
	return c
}

func toolNames(t *testing.T, c *rpcClient) []string {
	t.Helper()
	res := c.call("tools/list", map[string]any{})
	result, ok := res["result"].(map[string]any)
	require.True(t, ok, res)
	list, _ := result["tools"].([]any)
	names := make([]string, 0, len(list))
	for _, item := range list {
		tool, _ := item.(map[string]any)
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestBuildCompilesTheGeneratedServer(t *testing.T) {
	dir := demoModule(t)
	out := filepath.Join(t.TempDir(), "demo")

	require.NoError(t, runner.Build(t.Context(), dir, out, ""))
	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.False(t, info.IsDir())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"go.mod", "go.sum", "api.go"}, names, "nothing is written into the user's module")
}

func TestBuildReportsAnnotationErrors(t *testing.T) {
	dir := demoModule(t)
	bad := strings.Replace(demoSource, "// @MCP.Tool(\"hello\"", "// @MCP.Tool(\"hello", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(bad), 0o600))

	err := runner.Build(t.Context(), dir, filepath.Join(t.TempDir(), "demo"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api.go:")
}

func TestBuildReportsCompileErrors(t *testing.T) {
	dir := demoModule(t)
	broken := demoSource + "\nfunc Broken() int { return \"not an int\" }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(broken), 0o600))

	err := runner.Build(t.Context(), dir, filepath.Join(t.TempDir(), "demo"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an int")
}

func TestServeRunsTheServer(t *testing.T) {
	dir := demoModule(t)
	addr := freeAddr(t)
	t.Setenv("GOMCP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runner.Serve(ctx, runner.Options{Dir: dir, Stdout: &logs, Stderr: &logs})
	}()

	c := waitFor(t, "http://"+addr+"/mcp")
	assert.Equal(t, []string{"hello"}, toolNames(t, c))
	res := c.call("tools/call", map[string]any{keyName: "hello", keyArguments: map[string]any{keyName: "world"}})
	result, ok := res["result"].(map[string]any)
	require.True(t, ok, res)
	content, _ := result["content"].([]any)
	require.Len(t, content, 1)
	first, _ := content[0].(map[string]any)
	assert.Equal(t, "Hello world", first["text"])

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not stop")
	}
	assert.Contains(t, logs.String(), "demo setup", "the application output is forwarded")
}

func TestServeUsesTheAddrOption(t *testing.T) {
	dir := demoModule(t)
	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runner.Serve(ctx, runner.Options{Dir: dir, Addr: addr, Stdout: io.Discard, Stderr: io.Discard})
	}()
	_ = waitFor(t, "http://"+addr+"/mcp")
	cancel()
	require.NoError(t, <-done)
}

func TestServeLoadsDotEnv(t *testing.T) {
	dir := demoModule(t)
	addr := freeAddr(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("GOMCP_ADDR="+addr+"\n"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runner.Serve(ctx, runner.Options{Dir: dir, Stdout: io.Discard, Stderr: io.Discard})
	}()
	_ = waitFor(t, "http://"+addr+"/mcp")
	cancel()
	require.NoError(t, <-done)
}

func TestWatchRebuildsOnChange(t *testing.T) {
	dir := demoModule(t)
	addr := freeAddr(t)
	t.Setenv("GOMCP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runner.Watch(ctx, runner.Options{
			Dir: dir, Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	c := waitFor(t, "http://"+addr+"/mcp")
	assert.Equal(t, []string{"hello"}, toolNames(t, c))

	extra := demoSource + `
// @MCP.Tool("bye", description="Says bye")
func Bye(ctx mcp.Context, in HelloIn) (string, error) { return "Bye " + in.Name, nil }
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(extra), 0o600))

	require.Eventually(t, func() bool {
		return len(tryToolNames(t, "http://"+addr+"/mcp")) == 2
	}, 60*time.Second, 300*time.Millisecond, "the new tool did not show up after the change")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Watch did not stop")
	}
}

func TestWatchKeepsServingWhenARebuildFails(t *testing.T) {
	dir := demoModule(t)
	addr := freeAddr(t)
	t.Setenv("GOMCP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	logs := &syncBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	done := make(chan error, 1)
	go func() {
		done <- runner.Watch(ctx, runner.Options{
			Dir: dir, Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	c := waitFor(t, "http://"+addr+"/mcp")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.go"), []byte(demoSource+"\nfunc Broken() int { return \"x\" }\n"), 0o600))
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "build failed") }, 30*time.Second, 100*time.Millisecond)

	res := c.call("tools/list", map[string]any{})
	assert.Contains(t, res, "result", "the old server is still running")
	assert.Contains(t, logs.String(), "build failed")

	cancel()
	require.NoError(t, <-done)
}

// syncBuffer is a log sink that can be written and read from different goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestExecAvailable(t *testing.T) {
	_, err := exec.LookPath("go")
	require.NoError(t, err, "the go tool is required")
}

// tryToolNames lists the tools without failing the test while the server is restarting.
func tryToolNames(t *testing.T, url string) []string {
	t.Helper()
	body, err := json.Marshal(map[string]any{keyJSONRPC: jsonRPC, "id": 1, keyMethod: "tools/list", keyParams: withMeta(map[string]any{}, map[string]any{})})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Protocol-Version", specVersion)
	req.Header.Set("Mcp-Method", "tools/list")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	names := make([]string, 0, len(out.Result.Tools))
	for _, tool := range out.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}
