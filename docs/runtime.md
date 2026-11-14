# Runtime API

Implements MCP specification **2026-07-28**. `pkg/mcp` is the runtime. The annotations only generate calls to it, so everything can also be done by hand:

```go
srv := mcp.NewServer(mcp.Info{Name: "notes", Version: "1.0.0"}, mcp.WithPageSize(50))
err := mcp.AddTool(srv, mcp.ToolDef{Name: "add_note", Description: "Adds a note"}, Add) // Add(ctx mcp.Context, in AddIn) (Note, error)

mux := http.NewServeMux()
srv.Mount(mux, "/mcp") // also serves the OAuth protected resource metadata if configured
log.Fatal(http.ListenAndServe(":8080", mux))
```

`AddResource`, `AddStaticResource`, `AddPrompt`, `srv.AddResourceLister`, `srv.AddPromptCompletion`,
`srv.AddResourceCompletion`, `srv.Use` register the other features.

## Calls on `ctx`

| Call | MCP feature |
|---|---|
| `ctx.Log()` (`Debug` / `Info` / `Warn` / `Error`, slog-like) | log notifications on the response stream; only sent when the request has a `logLevel` in `_meta` (logging is deprecated in the spec) |
| `ctx.Progress(cur, total, msg)` | progress, only if the client sent a progress token |
| `ctx.Err()` / `ctx.Done()` | cancellation (`notifications/cancelled`, `tasks/cancel`, a closed connection) |
| `ctx.Principal()` | the authenticated caller (`Subject`, `Scopes`) |
| `ctx.Sample(mcp.SampleReq{...})` (deprecated in the spec) | sampling: `System`, `Messages`, `MaxTokens`, `Prefs`, `Temperature`, `StopSequences`, `Tools` + `ToolChoice` (needs `sampling.tools`); result `Text`, `Content`, `Model`, `StopReason` |
| `mcp.Elicit[T](ctx, msg)` | form elicitation: returns `(T, action, err)`; action is `mcp.Accept`, `mcp.Decline` or `mcp.Cancel`; `T` is a flat struct (`[]string` + `enum` = multi select) |
| `ctx.ElicitURL(msg, url)` / `ctx.ElicitDone(id)` | URL elicitation: `{id}` in `url` is replaced by the elicitation id; returns `mcp.ErrDeclined` if the user said no |
| `mcp.CompleteElicitation(id)` | called by the backend of the page the user was sent to; unblocks `ElicitDone` (the spec has no completion notification any more) |
| `ctx.Roots()` | the client's roots (deprecated in the spec) |
| `ctx.ResourceUpdated(uri)` | tells the listeners (`subscriptions/listen`) subscribed to the resource |
| `ctx.ResourcesChanged()` / `ToolsChanged()` / `PromptsChanged()` | `list_changed`, for features that appear or disappear at runtime |

### Asking the client

`Sample`, `Roots`, `Elicit` and `ElicitURL` return `mcp.ErrInputRequired` until the client has answered: return it
unchanged and ask first, act after ([tools.md](tools.md#asking-the-user-or-the-clients-model)).
They fail with an error wrapping `mcp.ErrCapability` if the client lacks the capability; `@MCP.Requires` fails
earlier with `-32021`. Set `GOMCP_STATE_KEY` (or `WithStateKey`) when several instances serve one endpoint.

## Results

| Helper | Use |
|---|---|
| a struct | `structuredContent`, `outputSchema` and a text block |
| a string, number or slice | text content only |
| `mcp.Result[T]` | the structured value plus your own content: `mcp.Text`, `mcp.ResourceLink`, `mcp.EmbedResource` |
| `mcp.Contents` with `mcp.TextContent`, `mcp.JSONContents` | resource contents; URI and MIME type are filled in |
| `mcp.Messages` with `mcp.UserText`, `mcp.User` | prompt messages |
| `mcp.Completion{Values, Total, HasMore}`, `mcp.Values(...)` | completion results (at most 100 values are sent) |
| `mcp.Paginate(items, page)` | one page of a list with an opaque cursor |

## Errors

| Return | What the client gets |
|---|---|
| `mcp.ToolError(msg)` or any other error from a tool | the tool result with `isError: true`; the model sees the message |
| `mcp.ErrInvalidParams(msg)`, `mcp.ErrNotFound` | a JSON-RPC protocol error (`ErrNotFound` is `-32602` with `data.uri`) |
| any other error from a resource, prompt or completion | `-32603` internal error |

Invalid tool arguments (missing required fields, wrong types, `enum`, `min`, `max`) are reported as tool errors,
so the model can correct itself. Error codes: `-32700` parse, `-32600` invalid request, `-32601` method not found,
`-32602` invalid params (also resource not found), `-32603` internal, `-32020` header mismatch,
`-32021` missing required client capability, `-32022` unsupported protocol version.

## Options

| Option | Effect |
|---|---|
| `WithInstructions(text)` | `instructions` of the `server/discover` result |
| `WithAllowedOrigins(...)` | accepted `Origin` values; by default only localhost origins |
| `WithKeepAlive(d)` | SSE keep-alive interval of listen streams |
| `WithPageSize(n)` | page size of all list operations |
| `WithBaseURL(url)` | base of relative icon sources; default: scheme and host of the request |
| `WithCache(ttl, scope)` | `ttlMs` and `cacheScope` (`mcp.CachePublic` / `mcp.CachePrivate`) of discover, list and read results |
| `WithStateKey(key)` | secret that protects `requestState`; share it between instances |
| `WithAuth(schemes...)` | see [auth.md](auth.md) |

## Automatic

`server/discover` (supported versions, capabilities, instructions, `serverInfo`), validation of the protocol
version, client capabilities and the `Mcp-Protocol-Version`, `Mcp-Method` and `Mcp-Name` headers (`-32020`
/ `-32022`), `x-mcp-header` parameters (`header:"Name"` tag, `Mcp-Param-*`), `resultType` on every result, cache hints,
`subscriptions/listen` (list changes, resource updates, task updates), JSON-RPC ids and errors, `202` for notifications,
`outputSchema` and `structuredContent` for struct results, argument validation, `Origin` check, the protected
resource metadata endpoint, `401` / `403` challenges, scope checks, the tasks extension (`tasks/get`, `tasks/update`,
`tasks/cancel`, status notifications), relative icon resolution with MIME types from the file extension.

Not the server's job: client registration (CIMD, DCR), PKCE and issuing tokens; these belong to the external
authorization server. Not supported, because the 2026-07-28 revision removed them: sessions and `initialize`, `ping`,
`logging/setLevel`, `resources/subscribe`, the GET stream and `Last-Event-ID` resumability, server to client requests,
JSON-RPC batching and the legacy HTTP+SSE transport. Older protocol revisions are rejected with `-32022`.
