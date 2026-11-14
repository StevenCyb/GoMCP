// Example: server identity, instructions, icon and transport settings (endpoint,
// allowed origin, keep-alive for streams, paging, cache hints, global middleware).

// @MCP.Server("notes", title="Notes MCP Example", version="1.0.0", website="https://example.com/notes")
// @MCP.Icon("assets/notes.svg")
// @MCP.Instructions("Use the notes tools to create and read short text notes.")
// @MCP.Endpoint("/mcp")
// @MCP.AllowedOrigin("https://claude.ai")
// @MCP.KeepAlive("30s")
// @MCP.PageSize(50)
// @MCP.Cache("5m", scope="private")
// @MCP.Use(api.Logging)
package api
