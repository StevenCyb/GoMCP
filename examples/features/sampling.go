// Example: sampling. The server has no model of its own; it asks the model of
// the client. The tool declares the needed client capability with Requires.

package api

import (
	"slices"
	"strings"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// Example: the client's model decides which tag fits a note (sampling). The
// server needs no API key: ctx.Sample sends the prompt to the client and
// returns the model's answer. The answer is untrusted text, so check it.
//
// @MCP.Tool("suggest_tag", description="Lets the model of the client pick a tag for a note")
// @MCP.Requires(sampling)
func SuggestTag(ctx mcp.Context, in IDIn) (Note, error) {
	note, ok := notes[in.ID]
	if !ok {
		return Note{}, mcp.ToolError("note not found")
	}

	res, err := ctx.Sample(mcp.SampleReq{
		System:    "Reply with exactly one word: work, private or idea.",
		Messages:  []mcp.SampleMsg{mcp.SampleText(mcp.RoleUser, note.Text)},
		MaxTokens: 10,
	})
	if err != nil {
		return Note{}, err // returns ErrInputRequired on the first run: return it as is
	}

	tag := strings.ToLower(strings.TrimSpace(res.Text))
	if !slices.Contains([]string{"work", "private", "idea"}, tag) {
		return Note{}, mcp.ToolError("the model did not return a valid tag")
	}
	note.Tag = tag
	notes[note.ID] = note
	return note, nil
}
