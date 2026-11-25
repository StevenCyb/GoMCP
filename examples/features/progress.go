// Example: progress and logs. Both are sent to the client on the response
// stream of the running call, and only if the client asked for them.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type AddManyIn struct {
	Texts []string `json:"texts" desc:"Bodies of the notes to add"`
}

type AddManyOut struct {
	Added int `json:"added"`
}

// Example: a tool that works through a list. ctx.Progress tells the client how
// far it is (only sent when the client sent a progress token). ctx.Log() sends
// log messages to the client (only those at or above the log level of the
// request). Neither is the server's own log: use the log package for that.
//
// @MCP.Tool("add_many_notes", description="Adds several notes and reports progress")
func AddMany(ctx mcp.Context, in AddManyIn) (AddManyOut, error) {
	ctx.Log().Info("adding notes", "count", len(in.Texts))
	for i, text := range in.Texts {
		if ctx.Err() != nil { // the client cancelled the request
			return AddManyOut{Added: i}, ctx.Err()
		}
		note := Note{ID: newID(), Text: text}
		notes[note.ID] = note
		ctx.Log().Debug("note added", "id", note.ID)
		ctx.Progress(float64(i+1), float64(len(in.Texts)), "added "+note.ID)
	}
	ctx.Log().Info("done", "count", len(in.Texts))
	return AddManyOut{Added: len(in.Texts)}, nil
}
