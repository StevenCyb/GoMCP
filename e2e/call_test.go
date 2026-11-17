package e2e_test

// TestCall runs gomcp call against example/: it builds it and talks over the stdio pipes like the command does.

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const toolAdd = "add_note"

func TestCall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the example")
	}
	run := func(req caller.Request) (string, error) {
		var out bytes.Buffer
		err := caller.Run(t.Context(), runner.Options{Dir: "../example", Stderr: io.Discard}, req, &out)
		return out.String(), err
	}

	listing, err := run(caller.Request{})
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^add_note +\S`, listing)
	assert.Contains(t, listing, "required: text")

	added, err := run(caller.Request{Tool: toolAdd, Args: json.RawMessage(`{"text":"from call"}`)})
	require.NoError(t, err)
	assert.Contains(t, added, "from call")

	// every call starts its own server: the notes of the first are gone, so the failing call is a real one
	_, err = run(caller.Request{Tool: "get_note", Args: json.RawMessage(`{"id":"missing"}`)})
	var toolErr *caller.ToolError
	require.ErrorAs(t, err, &toolErr, "a failing tool is exit code 1 in the command")
	assert.NotEmpty(t, toolErr.Message)

	raw, err := run(caller.Request{Tool: toolAdd, Args: json.RawMessage(`{"text":"json"}`), JSON: true})
	require.NoError(t, err)
	assert.Regexp(t, `(?s)^\{\n  .*"content"`, raw)

	var rpcErr *caller.RPCError
	_, err = run(caller.Request{Tool: "no_such_tool"})
	require.ErrorAs(t, err, &rpcErr)

	t.Run("a tool that asks the client for input", func(t *testing.T) {
		var inputErr *caller.InputError
		_, err := run(caller.Request{Tool: "delete_note", Args: json.RawMessage(`{"id":"1"}`)})
		require.ErrorAs(t, err, &inputErr)
		assert.Contains(t, err.Error(), "elicitation")

		_, err = run(caller.Request{Tool: toolImport})
		require.ErrorAs(t, err, &inputErr)
		assert.Contains(t, err.Error(), "--root")

		done, err := run(caller.Request{Tool: toolImport, Roots: []string{t.TempDir()}})
		require.NoError(t, err, "roots are answered with --root")
		assert.Contains(t, done, "1")
	})
}
