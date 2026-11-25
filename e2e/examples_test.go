// Examples test: build and run each small example over stdio and make one meaningful call.

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExamples(t *testing.T) {
	// the environment of the test process reaches the server, see runner.environment
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("file body"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), []byte("deep body"), 0o600))
	t.Setenv("FILES_ROOT", dir)
	t.Setenv("TODO_FILE", filepath.Join(t.TempDir(), "todo.json"))
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page body")) }))
	t.Cleanup(web.Close)
	t.Setenv("FETCH_ALLOW", "127.0.0.1")

	tests := map[string]func(t *testing.T, c *stdioClient){
		"hello": func(t *testing.T, c *stdioClient) {
			t.Helper()
			res := mustTool(t, c, "hello", obj{"name": "Ada"})
			assert.Equal(t, "Hello Ada", res["content"].([]any)[0].(obj)[keyText], "%v", res)
		},
		"files": func(t *testing.T, c *stdioClient) {
			t.Helper()
			found := structured(t, mustTool(t, c, "search_files", obj{"query": "HELLO"}))
			assert.Equal(t, []any{"hello.txt"}, found["files"])

			read, rpcErr, _ := c.call("resources/read", obj{keyURI: "file:///hello.txt"}, nil)
			require.Nil(t, rpcErr)
			assert.Equal(t, "file body", read["contents"].([]any)[0].(obj)[keyText])

			_, rpcErr, _ = c.call("resources/read", obj{keyURI: "file:///sub/deep.txt"}, nil)
			assert.NotNil(t, rpcErr, "a template variable matches one path segment, so subdirectory files are not reachable")

			_, rpcErr, _ = c.call("resources/read", obj{keyURI: "file:///../secret.txt"}, nil)
			assert.NotNil(t, rpcErr, "a path outside the root is not found")
			res := mustTool(t, c, "list_files", obj{"dir": "../.."})
			assert.True(t, isError(res), "%v", res)
		},
		"todo": func(t *testing.T, c *stdioClient) {
			t.Helper()
			mustTool(t, c, "add_todo", obj{keyText: "write tests"})
			mustTool(t, c, "add_todo", obj{keyText: "ship"})
			mustTool(t, c, "complete_todo", obj{"id": 1})

			read, rpcErr, _ := c.call("resources/read", obj{keyURI: "todo:///open"}, nil)
			require.Nil(t, rpcErr)
			assert.Contains(t, read["contents"].([]any)[0].(obj)[keyText], "ship")
			assert.NotContains(t, read["contents"].([]any)[0].(obj)[keyText], "write tests")

			// a client that listens to the open list is told when it changes
			c.send("subscriptions/listen", obj{"notifications": obj{"resourceSubscriptions": []any{"todo:///open"}}})
			assert.Equal(t, "notifications/subscriptions/acknowledged", c.next()[keyMethod])
			_, rpcErr, notes := c.call("tools/call", obj{keyName: "add_todo", keyArguments: obj{keyText: "watched"}}, nil)
			require.Nil(t, rpcErr)
			updated := notesOf(notes, "notifications/resources/updated")
			if len(updated) == 0 {
				updated = append(updated, c.next())
			}
			assert.Equal(t, "todo:///open", updated[0][keyParams].(obj)[keyURI])

			prompt, rpcErr, _ := c.call("prompts/get", obj{keyName: "plan_my_day"}, nil)
			require.Nil(t, rpcErr)
			assert.Contains(t, prompt["messages"].([]any)[0].(obj)["content"].(obj)[keyText], "ship")
		},
		"fetch": func(t *testing.T, c *stdioClient) {
			t.Helper()
			page := structured(t, mustTool(t, c, "fetch_url", obj{"url": web.URL}))
			assert.Equal(t, "page body", page["body"])

			res := mustTool(t, c, "fetch_url", obj{"url": "http://example.invalid/"})
			assert.True(t, isError(res), "a host outside FETCH_ALLOW is refused: %v", res)
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			run(t, startStdio(t, "../examples/"+name))
		})
	}
}
