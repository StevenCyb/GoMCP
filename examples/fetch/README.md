# fetch

Tool `fetch_url`: GET a page and return it as text.

- Only `http` and `https`, only hosts in `FETCH_ALLOW` (comma separated). **Empty denies everything**, and the error says so.
- 10 second timeout, at most 256 KiB of body (the result says `truncated`), redirects are checked against the allowlist again.
- Failures are `mcp.ToolError`s, so the model sees the reason. No client feature is required, so HTTP and stdio both work.

```sh
FETCH_ALLOW=example.com,go.dev gomcp serve ./examples/fetch
```

Client config for stdio (build once, `gomcp stdio` compiles on every start):

```sh
gomcp build ./examples/fetch ./bin/fetch
```

```json
{
  "mcpServers": {
    "fetch": { "command": "/absolute/path/to/bin/fetch", "args": ["--stdio"], "env": { "FETCH_ALLOW": "example.com,go.dev" } }
  }
}
```
