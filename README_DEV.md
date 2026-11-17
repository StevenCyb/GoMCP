# Handbook for developer

## Checks

```sh
go vet ./...
golangci-lint run --path-mode=abs      # config: .golangci.yml, CI uses the latest release
go test -race ./...                    # includes e2e/, which builds and runs examples/ (about a minute)
for d in examples/*/; do (cd $d && go vet ./... && go test ./...); done   # every example is its own module
```

`e2e/` talks to `examples/features/` like an MCP client and fails when a feature of it has no test. A new feature needs
an example and an e2e check.

## Test new feature

Write a MCP with a new feature (or modify examples/features). Then run the service.
```bash
go run ./cmd/gomcp serve ./examples/features --addr 127.0.0.1:8765
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

## Quick manual test

`gomcp call` builds the project and lists its tools or calls one, no AI client needed:
```sh
go run ./cmd/gomcp call ./example
go run ./cmd/gomcp call ./example add_note '{"text":"hi"}'
```

## Test stdio

Over stdio the client starts the server itself, so build once and point the client at the binary:
```sh
go run ./cmd/gomcp build ./examples/features ./bin/notes
```
```json
{
  "mcpServers": {
    "mcp_example_stdio": { "command": "./bin/notes", "args": ["--stdio"] }
  }
}
```
By hand, one request per line (stdout must contain nothing but JSON lines, logs go to stderr):
```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}' | ./bin/notes --stdio
```
`e2e/stdio_test.go` runs the example over pipes, and `scripts/smoke.sh` checks `gomcp stdio` and the built binary against a published version.
