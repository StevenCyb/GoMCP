# files

A read-only browser for one directory, for a local client over stdio.

- Resource template `file:///{path}`: reads a text file (up to 1 MiB).
- Tool `list_files`: the entries of a directory.
- Tool `search_files`: files whose name contains a text.

The root is `FILES_ROOT` (default: the working directory). A path that leaves the root is refused
(`resolve` in `files.go`, tested in `files_test.go`). Symlinks inside the root are followed.

```sh
FILES_ROOT=$HOME/notes gomcp stdio ./examples/files
```

Client config (build once, `gomcp stdio` compiles on every start):

```sh
gomcp build ./examples/files ./bin/files
```

```json
{
  "mcpServers": {
    "files": { "command": "/absolute/path/to/bin/files", "args": ["--stdio"], "env": { "FILES_ROOT": "/home/me/notes" } }
  }
}
```
