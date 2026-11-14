# Handbook for developer

## Checks

```sh
go vet ./...
golangci-lint run --path-mode=abs      # config: .golangci.yml, CI uses the latest release
go test -race ./...                    # includes e2e/, which builds and runs example/ (about a minute)
(cd example && go vet ./...)           # example is its own module
```

`e2e/` talks to `example/` like an MCP client and fails when a feature of the example has no test. A new feature needs
an example and an e2e check.

## Test new feature

Write a MCP with a new feature (or modify example). Then run the service.
```bash
go run ./cmd/gomcp serve ./example --addr 127.0.0.1:8765
```

Create a `mcp.json` to configure the MCP connector if not present. The example's OAuth verifier is a stub that accepts
any non-empty token.
```json
{
  "mcpServers": {
    "mcp_example": {
      "type": "http",
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer demo" }
    }
  }
}
```

Start a new Claude instance with the MCP service.
```sh
claude --mcp-config mcp.json
```

Claude Code answers forms and URL pages itself in non-interactive runs, so use `e2e/` for exact checks of those paths.
