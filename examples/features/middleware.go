// Example: one middleware shape that wraps tools, resources, prompts and
// completion (here: logging).

package api

import (
	"log"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// Example: middleware shared by tools, resources, prompts and completion. It
// is attached with @MCP.Use (see server_transport.go); @MCP.Middleware only
// makes the parser check the signature.
//
// @MCP.Middleware
func Logging(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
	log.Printf("%s %s", req.Kind, req.Name)
	return next(ctx, req)
}
