# Security policy

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability reporting
(Security tab → Report a vulnerability) on this repository. You will get an answer as soon as possible.

## Scope notes

- gomcp is a resource server: it verifies tokens, it never issues them.
- `gomcp serve`, `watch` and `build` compile and run the project you point them at. Only use them on code you trust.
- The generated server listens on `GOMCP_ADDR` (default `:8080`, all interfaces). Set `127.0.0.1:8080` for local use and
  configure `@MCP.Auth` before exposing it.
- `example/` uses a stub token verifier that accepts every non-empty token. Never copy it into production.
- Features of the example that check scopes (`@MCP.AuthScopes`) apply to tools, prompts, resources, completion and
  resource subscriptions.
