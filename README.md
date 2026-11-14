# GoMCP

[![Release](https://img.shields.io/github/v/release/StevenCyb/GoMCP)](https://github.com/StevenCyb/GoMCP/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/StevenCyb/GoMCP)](go.mod)

Annotation-driven [MCP](https://modelcontextprotocol.io) server generator for
Go. Write plain handler functions, sprinkle `@MCP.*` comments on top, and
GoMCP generates a runnable `main` package serving a spec-compliant
Streamable HTTP MCP endpoint with tools, resources and prompts.

**Supported spec: MCP [2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28)** — the stateless
revision (no sessions, no `initialize`, every request carries its protocol version and client capabilities).
Older revisions are not supported.

## See It In 5 Seconds

Write this in a Go module that requires GoMCP (`go get github.com/StevenCyb/GoMCP`):

```go
package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type HelloIn struct {
	Name string `json:"name" desc:"Who to greet"`
}

// @MCP.Tool("hello", description="Greets someone")
func Hello(ctx mcp.Context, in HelloIn) (string, error) {
	return "Hello " + in.Name, nil
}
```

Run:

```sh
gomcp serve .
```

Get:

- A Streamable HTTP MCP endpoint at `/mcp` with `tools/list` and `tools/call`.
- Input and output schemas derived from your Go types.
- Auto-generated bootstrap (`main`, transport, auth, lifecycle wiring).

> A more advanced example is available in the [example](example) directory.

## Install

```sh
go install github.com/StevenCyb/GoMCP/cmd/gomcp@latest
```

The binary is called `gomcp`.

## Quick start

```sh
# watch project, regenerate and restart on every .go change
gomcp watch ./example

# generate + run once
gomcp serve ./example

# generate + compile to a single binary
gomcp build ./example ./bin/notes
```

See [docs/cli.md](docs/cli.md) for the full command and flag reference.

## Annotation syntax

```go
// Adds a short text note.
//
// @MCP.Tool("add_note", title="Add note", description="Adds a short text note")
// @MCP.AuthScopes("notes:write")
func Add(ctx mcp.Context, in AddIn) (Note, error) { ... }
```

- One optional positional `"primary"` (tool name, resource URI, path, ...); the rest is `key="value"`, `key=1` or a bare `flag`.
- The Go doc comment above is the description, unless `description=` is set. A period at the end of an annotation line is ignored.
- Schemas come from the signature types; hints are struct tags `desc enum min max default format`, required = no `omitempty` and not a pointer.

## Documentation

| Document                                   | What it covers                                                                             | Example                                                                                  |
| ------------------------------------------ | ------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------- |
| [docs/server.md](docs/server.md)           | Server identity, transport, middleware, lifecycle hooks, static assets and icons | [server_transport.go](example/server_transport.go), [lifecycle.go](example/lifecycle.go) |
| [docs/auth.md](docs/auth.md)               | `none` / `bearer` / `oauth`, scopes                                                        | [auth.go](example/auth.go)                                                               |
| [docs/tools.md](docs/tools.md)             | Tools, hints, tasks, elicitation, sampling, roots                                          | [tools.go](example/tools.go), [elicitation.go](example/elicitation.go)                   |
| [docs/resources.md](docs/resources.md)     | Resources, templates, subscriptions, listing                                               | [resources.go](example/resources.go)                                                     |
| [docs/prompts.md](docs/prompts.md)         | Prompts, and completion for prompt arguments and resource template variables               | [prompts_completion.go](example/prompts_completion.go)                                   |
| [docs/runtime.md](docs/runtime.md)         | `pkg/mcp` runtime: `mcp.Context` calls, results, errors, options, automatic behaviour      |                                                                                          |
| [docs/cli.md](docs/cli.md)                 | CLI commands and flags                                                                     |                                                                                          |

## License

MIT — see [LICENSE](LICENSE).
