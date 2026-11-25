# todo

A todo list that survives restarts.

- `Setup` loads the list from a JSON file, `Teardown` saves it when the server stops (end of input over stdio, Ctrl-C over HTTP).
- Tools `add_todo`, `list_todos`, `complete_todo`.
- Resource `todo:///open`: the open items. It is subscribable: adding or completing an item sends `notifications/resources/updated`.
- Prompt `plan_my_day`: asks the model to order the open items.

The file is `TODO_FILE` (default `./todo.json`). A missing file is an empty list, a broken file stops the start.
The list is written only on a clean stop, so a killed server loses its changes.

```sh
TODO_FILE=$HOME/todo.json gomcp serve ./examples/todo
```

Client config for stdio (build once, `gomcp stdio` compiles on every start):

```sh
gomcp build ./examples/todo ./bin/todo
```

```json
{
  "mcpServers": {
    "todo": { "command": "/absolute/path/to/bin/todo", "args": ["--stdio"], "env": { "TODO_FILE": "/home/me/todo.json" } }
  }
}
```
