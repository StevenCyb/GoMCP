// Example: server-initiated sampling (ask the client LLM); the tool declares
// the needed client capability with Requires.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

// Example: tool that asks the client's LLM for a completion (sampling).
//
// @MCP.Tool("summarize_all", description="Summarizes all notes using the client LLM")
// @MCP.Hint(readOnly)
// @MCP.Requires(sampling)
func SummarizeAll(ctx mcp.Context, _ mcp.Empty) (string, error) {
	var all string
	for _, note := range notes {
		all += note.Text + "\n"
	}

	res, err := ctx.Sample(mcp.SampleReq{
		System:    "Summarize in one sentence.",
		Messages:  []mcp.SampleMsg{mcp.SampleText(mcp.RoleUser, all)},
		MaxTokens: 100,
		Prefs:     mcp.ModelPrefs{Hints: []string{"claude"}, Speed: 0.8},
	})
	return res.Text, err
}
