# Changelog

## v0.1.0

First release.

- Annotation-driven generator: `gomcp build`, `serve` and `watch`; nothing is written into your module.
- Runtime `pkg/mcp` for MCP specification 2026-07-28 over Streamable HTTP: stateless requests, `server/discover`,
  tools, resources, prompts, completion, form and URL elicitation, sampling, roots, progress, logging,
  `subscriptions/listen`, tasks (extension), cache hints.
- Authentication: bearer and OAuth resource server with scopes.
- Release binaries for Linux, macOS and Windows (amd64, arm64). Windows is untested.
