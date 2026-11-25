// Example: long-running task-augmented tool that combines progress
// notifications, cancellation and the client's roots.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

// Example: long-running task that reads the client's roots, reports progress
// and handles cancellation. Progress reaches the client only when the call
// runs directly; when the client runs it as a task, the call has no effect.
//
// @MCP.Tool("import_notes", description="Imports one note per client root")
// @MCP.Task(optional, ttl="10m", poll="2s")
// @MCP.Requires(roots)
func Import(ctx mcp.Context, _ mcp.Empty) (int, error) {
	roots, err := ctx.Roots()
	if err != nil {
		return 0, err
	}
	for i, r := range roots {
		if ctx.Err() != nil { // cancelled / tasks/cancel
			return i, ctx.Err()
		}
		id := newID()
		notes[id] = Note{ID: id, Text: "imported from " + r.URI}
		ctx.Progress(float64(i+1), float64(len(roots)), "importing "+r.URI)
	}
	return len(roots), nil
}
