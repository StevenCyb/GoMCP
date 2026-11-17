// Example: serving embedded static files (svg, png, ...) from the MCP server;
// icons reference them by relative path.

package api

import "embed"

// Example: embedded files served by the server; icons reference them by a
// relative path like assets/add.svg.
//
// @MCP.ServeAssets("/assets")
//
//go:embed assets
var Assets embed.FS
