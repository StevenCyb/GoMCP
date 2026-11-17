# hello

The smallest server: one tool, `hello`. Start here.

```sh
gomcp serve ./examples/hello     # http://127.0.0.1:8080/mcp
gomcp stdio ./examples/hello     # stdin and stdout
```

Client config for stdio (build once, `gomcp stdio` compiles on every start):

```sh
gomcp build ./examples/hello ./bin/hello
```

```json
{ "mcpServers": { "hello": { "command": "/absolute/path/to/bin/hello", "args": ["--stdio"] } } }
```
