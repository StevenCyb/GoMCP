# Examples

Each directory is its own Go module with annotated Go files. Run one with `gomcp serve ./examples/<name>`
(HTTP) or `gomcp stdio ./examples/<name>` (stdin and stdout, for a client that starts it).

| Example | Shows | Transport |
| --- | --- | --- |
| [`hello`](hello) | the smallest server: one tool | HTTP or stdio |
| [`files`](files) | read-only directory browser: resource template, two tools, path traversal protection | HTTP or stdio |
| [`todo`](todo) | todo list in a JSON file: Setup and Teardown, tools, a prompt, a resource | HTTP or stdio |
| [`fetch`](fetch) | `fetch_url` with an allowlist, size limit and timeout, `ToolError` | HTTP or stdio |
| [`features`](features) | every feature once (auth, tasks, sampling, elicitation, ...), see its README | HTTP or stdio |
| [`esm`](esm) | ticket desk: Entra authentication with scopes per role, confirmation before every change, a state machine in a service on top of a repository, tests at every level, see its README | HTTP (auth), stdio for local tries |

The code is meant to be copied: copy a directory, change the module name and the `replace` line of its `go.mod`
(or drop it and `go get github.com/StevenCyb/GoMCP`).

`go test ./...` inside an example runs its unit tests. `e2e/` builds and runs every example through a test client.
