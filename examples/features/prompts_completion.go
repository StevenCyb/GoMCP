// Example: prompt with arguments and an embedded resource, plus static and
// dynamic argument completion.

package api

import (
	"strings"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type SummarizeArgs struct {
	ID    string `json:"id"`
	Style string `json:"style,omitempty"`
}

// Example: prompt with arguments, an embedded resource and static completion
// values.
//
// @MCP.Prompt("summarize_notes", title="Summarize note", description="Asks the LLM to summarize a note")
// @MCP.Complete(arg="style", values="short,bullets")
func SummarizeNotes(ctx mcp.Context, a SummarizeArgs) (mcp.Messages, error) {
	return mcp.Messages{
		mcp.UserText("Summarize this note (" + a.Style + "):"),
		mcp.User(mcp.EmbedResource("note:///" + a.ID)),
	}, nil
}

// Example: dynamic completion shared by a resource template and a prompt argument.
//
// @MCP.Complete(for="note:///{id}", arg="id")
// @MCP.Complete(for="summarize_notes", arg="id")
func CompleteID(ctx mcp.Context, partial string, _ map[string]string) (mcp.Completion, error) {
	var out []string
	for id := range notes {
		if strings.HasPrefix(id, partial) {
			out = append(out, id)
		}
	}
	return mcp.Completion{Values: out}, nil
}
