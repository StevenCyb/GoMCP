// Example: asking the user for input mid-call: form elicitation (confirmation,
// follow-up questions with several field types) and URL elicitation (browser
// flow).

package api

import (
	"time"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// Example: schema of the form shown to the user. Any flat struct works;
// mcp.Elicit builds the form from it by reflection.
type ConfirmDelete struct {
	Confirm bool `json:"confirm" desc:"Really delete this note?" default:"false"`
}

// Example: action that needs user confirmation (form elicitation).
//
// @MCP.Tool("delete_note", description="Deletes a note")
// @MCP.Hint(destructive)
// @MCP.AuthScopes("notes:write")
// @MCP.Requires(elicitation)
func Delete(ctx mcp.Context, in IDIn) (mcp.Empty, error) {
	ans, action, err := mcp.Elicit[ConfirmDelete](ctx, "Delete "+in.ID+"?")
	if err != nil {
		return mcp.Empty{}, err // returns ErrInputRequired on the first run: return it as is
	}
	if action != mcp.Accept || !ans.Confirm {
		return mcp.Empty{}, mcp.ToolError("not confirmed")
	}
	delete(notes, in.ID)
	ctx.ResourceUpdated("note:///" + in.ID) // notify subscribers
	return mcp.Empty{}, nil
}

// Example: generic follow-up question with several field types (form
// elicitation): enum, number with bounds, optional text, bool.
type NoteDetails struct {
	Tag      string `json:"tag"               desc:"Category"            enum:"work,private,idea"`
	Priority int    `json:"priority"          desc:"Priority"            min:"1" max:"5" default:"3"`
	Comment  string `json:"comment,omitempty" desc:"Optional comment"`
	Notify   bool   `json:"notify"            desc:"Notify on changes"   default:"false"`
}

// Example: asks the user for missing details and handles accept, decline and
// cancel (form elicitation).
//
// @MCP.Tool("detail_note", description="Asks the user for extra details and stores them on the note")
// @MCP.Requires(elicitation)
func Detail(ctx mcp.Context, in IDIn) (Note, error) {
	note, ok := notes[in.ID]
	if !ok {
		return Note{}, mcp.ToolError("note not found")
	}
	d, action, err := mcp.Elicit[NoteDetails](ctx, "Add details for "+in.ID)
	if err != nil {
		return Note{}, err
	}
	if action != mcp.Accept { // declined or cancelled: keep as is
		return note, nil
	}
	note.Tag, note.Priority, note.Comment, note.Notify = d.Tag, d.Priority, d.Comment, d.Notify
	notes[note.ID] = note
	return note, nil
}

// Example: sensitive flow handled outside the client (URL elicitation).
//
// @MCP.Tool("connect_account", description="Links an external account via browser")
// @MCP.Requires(elicitation="url")
func Connect(ctx mcp.Context, _ mcp.Empty) (mcp.Empty, error) {
	// {id} is replaced by the elicitation id. When the user is done, the backend
	// of the sign-in page tells an endpoint in this process (your own code, not
	// provided by gomcp), which calls mcp.CompleteElicitation(id).
	id, err := ctx.ElicitURL("Sign in to continue", "https://login.example.local/connect?e={id}")
	if err != nil {
		return mcp.Empty{}, err
	}
	// ElicitDone never closes for an expired id, so always wait with a way out.
	select {
	case <-ctx.ElicitDone(id):
		return mcp.Empty{}, nil
	case <-ctx.Done():
		return mcp.Empty{}, ctx.Err()
	case <-time.After(10 * time.Minute):
		return mcp.Empty{}, mcp.ToolError("sign-in timed out")
	}
}
