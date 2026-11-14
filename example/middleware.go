// Example: one middleware shape that wraps tools, resources, prompts and
// completion (here: logging).

package api

import (
	"log"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// Example: middleware shared by tools, resources, prompts and completion.
//
// @MCP.Middleware
func Logging(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
	log.Printf("%s %s", req.Kind, req.Name)
	return next(ctx, req)
}
