# Security policy

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability reporting
(Security tab → Report a vulnerability) on this repository. You will get an answer as soon as possible.

## Scope notes

- gomcp is a resource server: it verifies tokens, it never issues them.
- `gomcp serve`, `watch` and `build` compile and run the project you point them at. Only use them on code you trust.
- The generated server listens on `GOMCP_ADDR` (default `127.0.0.1:8080`, loopback only). Set `GOMCP_ADDR` (or
  `--addr`) to expose it, and configure `@MCP.Auth` first.
- Any error a tool returns, not only `mcp.ToolError`, is sent to the model as text. Never put secrets, DSNs or paths in it.
- `ctx.ResourceUpdated(uri)` reaches every listener subscribed to that exact URI (subject to the resource scopes). Use
  per-user URIs and authorize them in the resource handler via `ctx.Principal()`.
- `example/` uses a stub token verifier that accepts every non-empty token. Never copy it into production.
- Features of the example that check scopes (`@MCP.AuthScopes`) apply to tools, prompts, resources, completion and
  resource subscriptions.
