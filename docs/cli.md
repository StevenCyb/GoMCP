# CLI reference

## Commands

### `gomcp build <project_path> <output_path>`

Parses and generates the project, then compiles it to a single binary at
`output_path`.

### `gomcp serve <project_path>`

Generates, compiles and runs the project once. Blocks until `SIGINT`/`SIGTERM`.

### `gomcp watch <project_path>`

Watches `project_path` for `.go` file changes. On every change it regenerates,
recompiles and restarts the server automatically. Blocks until
`SIGINT`/`SIGTERM`.

### `gomcp version`

Prints the current version string.

---

## Flags for `build` / `serve` / `watch`

| Flag     | Short | Description                                    |
| -------- | ----- | ---------------------------------------------- |
| `--tags` | `-t`  | build tags forwarded to `go build` / `go run`  |

## Flags for `serve` / `watch`

| Flag       | Short | Description                                                       |
| ---------- | ----- | ----------------------------------------------------------------- |
| `--dotenv` | `-e`  | path to a `.env` file injected into the server process at startup |
| `--addr`   | `-a`  | listen address of the server, sets `GOMCP_ADDR` (default `:8080`) |

When `--dotenv` is omitted, `<module>/.env` is loaded if it exists. Variables that are already set in the
environment win over the file. The file supports `KEY=value`, `export KEY=value`, `#` comments and single or
double quoted values.

`serve` stops on `SIGINT` / `SIGTERM` and passes the signal on to the server so it can shut down gracefully.
`watch` keeps the previous server running when a rebuild fails.

Unknown flags are ignored by the command line library.
