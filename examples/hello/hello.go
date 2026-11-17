// Example: the smallest server, one tool.

// @MCP.Server("hello", version="1.0.0")
package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type HelloIn struct {
	Name string `json:"name" desc:"Who to greet"`
}

// @MCP.Tool("hello", description="Greets someone")
func Hello(ctx mcp.Context, in HelloIn) (string, error) {
	return "Hello " + in.Name, nil
}
