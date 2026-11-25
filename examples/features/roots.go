// Example: roots. The client tells the server which folders it works in (for
// example the project open in an IDE). The tool declares the needed client
// capability with Requires.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

// RootsOut is the list of folders the client works in.
type RootsOut struct {
	Roots []string `json:"roots"`
}

// Example: which folders is the client working in? ctx.Roots asks the client
// and returns what the user has open, so a tool does not have to ask for a
// path. The URIs point to the client's machine (file:///...), which a remote
// server cannot read; over stdio the server runs on the same machine.
//
// @MCP.Tool("list_roots", description="Lists the folders the client works in")
// @MCP.Hint(readOnly)
// @MCP.Requires(roots)
func ListRoots(ctx mcp.Context, _ mcp.Empty) (RootsOut, error) {
	roots, err := ctx.Roots()
	if err != nil {
		return RootsOut{}, err // returns ErrInputRequired on the first run: return it as is
	}
	out := RootsOut{Roots: []string{}}
	for _, r := range roots {
		out.Roots = append(out.Roots, r.URI)
	}
	return out, nil
}
