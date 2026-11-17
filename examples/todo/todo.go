// Example: a todo list that survives restarts. Setup loads it from a JSON file and
// Teardown saves it. The path comes from TODO_FILE (default: ./todo.json).

// @MCP.Server("todo", version="1.0.0")
// @MCP.Instructions("Keep a todo list: add, list and complete items.")
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type Item struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

var (
	mu    sync.Mutex
	items []Item
	path  string
)

// load reads the list; a missing file is an empty list.
func load(path string) ([]Item, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Item
	return out, json.Unmarshal(data, &out)
}

func save(path string, items []Item) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func open(items []Item) []Item {
	out := []Item{}
	for _, it := range items {
		if !it.Done {
			out = append(out, it)
		}
	}
	return out
}

// Example: load the file once on startup; an unreadable file stops the start.
//
// @MCP.Setup
func Setup() (err error) {
	if path = os.Getenv("TODO_FILE"); path == "" {
		path = "todo.json"
	}
	items, err = load(path)
	return err
}

// Example: write the list once on shutdown. Teardown cannot return an error, so it logs.
//
// @MCP.Teardown
func Teardown() {
	mu.Lock()
	defer mu.Unlock()
	if err := save(path, items); err != nil {
		fmt.Fprintln(os.Stderr, "todo: cannot save:", err)
	}
}

type AddIn struct {
	Text string `json:"text" desc:"What to do" min:"1" max:"200"`
}

type IDIn struct {
	ID int `json:"id" desc:"Id of the item"`
}

type List struct {
	Items []Item `json:"items"`
}

// @MCP.Tool("add_todo", description="Adds an item to the list")
func Add(ctx mcp.Context, in AddIn) (Item, error) {
	mu.Lock()
	defer mu.Unlock()
	it := Item{ID: len(items) + 1, Text: in.Text}
	items = append(items, it)
	ctx.ResourcesChanged()
	return it, nil
}

// @MCP.Tool("list_todos", description="Lists all items, done and open")
// @MCP.Hint(readOnly)
func ListAll(ctx mcp.Context, _ struct{}) (List, error) {
	mu.Lock()
	defer mu.Unlock()
	return List{Items: append([]Item{}, items...)}, nil
}

// @MCP.Tool("complete_todo", description="Marks an item as done")
func Complete(ctx mcp.Context, in IDIn) (Item, error) {
	mu.Lock()
	defer mu.Unlock()
	for i := range items {
		if items[i].ID == in.ID {
			items[i].Done = true
			ctx.ResourcesChanged()
			return items[i], nil
		}
	}
	return Item{}, mcp.ToolError("no such item")
}

// @MCP.Resource("todo:///open", mime="application/json", description="The open items")
func Open(ctx mcp.Context) (mcp.Contents, error) {
	mu.Lock()
	defer mu.Unlock()
	return mcp.JSONContents(open(items))
}

// @MCP.Prompt("plan_my_day", description="Asks the LLM to plan the day around the open items")
func PlanMyDay(ctx mcp.Context, _ struct{}) (mcp.Messages, error) {
	mu.Lock()
	defer mu.Unlock()
	var b strings.Builder
	for _, it := range open(items) {
		fmt.Fprintf(&b, "- %s\n", it.Text)
	}
	if b.Len() == 0 {
		b.WriteString("(nothing open)\n")
	}
	return mcp.Messages{mcp.UserText("Plan my day. Order these open todos and suggest a time for each:\n" + b.String())}, nil
}
