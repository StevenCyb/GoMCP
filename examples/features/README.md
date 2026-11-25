# Features example

A notes server that uses every GoMCP feature once. The annotations are the
documentation: read a file next to the README section named below.

```sh
gomcp serve ./examples/features    # MCP endpoint on http://127.0.0.1:8080/mcp
gomcp stdio ./examples/features    # the same server over stdin and stdout, for a client that starts it
```

Over stdio there is no authentication (see "Local or remote" in the main README).

The OAuth verifier in `auth.go` is a **stub**: it accepts any non-empty bearer
token. Never use it as it is. `e2e/` builds and runs it and checks
every feature through a test client, so it stays working.

| File | Shows | README section |
| --- | --- | --- |
| `server_transport.go` | identity, instructions, icon, endpoint, origin, keep-alive, paging, cache hints, global middleware | Server |
| `middleware.go` | one middleware for tools, resources, prompts, completion | Middleware |
| `lifecycle.go`, `model.go` | setup and teardown, shared state | Setup and teardown |
| `static_assets.go` | embedded assets, icons by relative path | Static assets and icons |
| `auth.go` | OAuth resource server, API key, scopes | Authentication |
| `tools.go` | typed input/output, schema tags, hints, results, errors, `_meta` | Tools |
| `elicitation.go` | form and URL elicitation, confirmation, the sign-in page and its completion as `@MCP.HTTPHandler` | Asking the user (forms), URL elicitation, Custom HTTP handlers |
| `sampling.go` | sampling: the client's model picks a tag, answer checked, `Requires` | Sampling |
| `progress.go` | progress and log notifications while a tool runs | Progress and logs |
| `roots.go` | roots: the folders the client works in, `Requires` | Roots |
| `task_import.go` | tasks, progress, cancellation, roots | Long-running tasks, Roots |
| `resources.go` | templates, subscriptions, annotations, listing | Resources |
| `prompts_completion.go` | prompts, embedded resource, static and dynamic completion | Prompts and completion |
