package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// postToolCall sends one stateless tools/call request to the server and returns the result as JSON.
func postToolCall(url, name, arguments string) string {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `,` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", name)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var msg struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return string(raw)
	}
	return string(msg.Result)
}

type exampleGreetIn struct {
	Name string `desc:"Who to greet" json:"name"`
}

func ExampleNewServer() {
	srv := mcp.NewServer(mcp.Info{Name: "hello", Version: "1.0.0"})
	_ = mcp.AddTool(srv, mcp.ToolDef{Name: "hello"}, func(_ mcp.Context, in exampleGreetIn) (string, error) {
		return "Hello " + in.Name, nil
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	fmt.Println(postToolCall(ts.URL, "hello", `{"name":"Ada"}`))
	// Output:
	// {"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"hello","version":"1.0.0"}},"content":[{"type":"text","text":"Hello Ada"}],"resultType":"complete"}
}

func ExampleAddTool() {
	type sumIn struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type sumOut struct {
		Sum int `json:"sum"`
	}
	srv := mcp.NewServer(mcp.Info{Name: "calc", Version: "1.0.0"})
	err := mcp.AddTool(srv, mcp.ToolDef{Name: "sum", Description: "Adds two numbers"},
		func(_ mcp.Context, in sumIn) (sumOut, error) { return sumOut{Sum: in.A + in.B}, nil })
	fmt.Println(err)

	ts := httptest.NewServer(srv)
	defer ts.Close()
	fmt.Println(postToolCall(ts.URL, "sum", `{"a":2,"b":3}`))
	// Output:
	// <nil>
	// {"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"calc","version":"1.0.0"}},"content":[{"type":"text","text":"{\"sum\":5}"}],"resultType":"complete","structuredContent":{"sum":5}}
}

func ExamplePaginate() {
	items := []string{"a", "b", "c", "d", "e"}
	page := mcp.Paginate(items, mcp.PageReq{Size: 2})
	fmt.Println(page.Items, page.Next != "")
	page = mcp.Paginate(items, mcp.PageReq{Cursor: page.Next, Size: 2})
	fmt.Println(page.Items, page.Next != "")
	page = mcp.Paginate(items, mcp.PageReq{Cursor: page.Next, Size: 2})
	fmt.Println(page.Items, page.Next != "")
	// Output:
	// [a b] true
	// [c d] true
	// [e] false
}
