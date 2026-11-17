// Example: tools with typed input/output (schemas, structured content), icon,
// scope, _meta, logging, read-only hints, rich results with resource links, and
// tool errors.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type AddIn struct {
	Text string `json:"text"          desc:"Note body" min:"1" max:"500"`
	Tag  string `json:"tag,omitempty" desc:"Category"  enum:"work,private,idea" default:"idea"`
}

// Example: typed input/output (schemas from structs), icon, required scope,
// custom metadata and logging to the client.
//
// @MCP.Tool("add_note", title="Add note", description="Adds a short text note and returns it")
// @MCP.Icon("assets/add.svg")
// @MCP.AuthScopes("notes:write")
// @MCP.Meta(team="docs")
func Add(ctx mcp.Context, in AddIn) (Note, error) {
	note := Note{ID: newID(), Text: in.Text, Tag: in.Tag}
	notes[note.ID] = note

	ctx.Log().Info("note added", "id", note.ID, "by", ctx.Principal().Subject)
	ctx.ResourcesChanged() // new note => resources/list_changed
	return note, nil
}

// Example: read-only tool with a rich result (text + resource link) and a tool
// execution error.
//
// @MCP.Tool("get_note", description="Returns a note by id")
// @MCP.Hint(readOnly, idempotent)
// @MCP.AuthScopes("notes:read")
func Get(ctx mcp.Context, in IDIn) (mcp.Result[Note], error) {
	note, ok := notes[in.ID]
	if !ok {
		return mcp.Result[Note]{}, mcp.ToolError("note not found")
	}
	return mcp.Result[Note]{
		Structured: note,
		Content:    []mcp.Content{mcp.Text(note.Text), mcp.ResourceLink("note:///" + note.ID)},
	}, nil
}
