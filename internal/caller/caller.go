// Package caller is a tiny MCP client for the terminal: it lists the tools of a stdio server and calls
// one, so a project can be tried without an AI client.
package caller

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	specVersion = "2026-07-28"
	metaVersion = "io.modelcontextprotocol/protocolVersion"
	metaCaps    = "io.modelcontextprotocol/clientCapabilities"
	maxRounds   = 8 // input_required retries before giving up
)

// RPCError is a JSON-RPC error of the server.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("error %d: %s", e.Code, e.Message) }

// ToolError is a tool that ran and reported isError.
type ToolError struct{ Message string }

func (e *ToolError) Error() string { return "tool error: " + e.Message }

// InputError is a tool that needs something from the client that this client cannot give.
type InputError struct{ Needs []string }

func (e *InputError) Error() string {
	return "the tool needs input from the client (" + strings.Join(e.Needs, "; ") + "), which gomcp call cannot answer"
}

// Tool is a tool as listed by the server.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"inputSchema"`
}

// Client speaks the stateless protocol over a stdio pipe pair.
type Client struct {
	w     io.Writer
	sc    *bufio.Scanner
	id    int
	roots []string // directories answered to roots/list
}

// New returns a client that writes requests to w and reads responses from r. roots are the directories
// it answers to roots/list.
func New(w io.Writer, r io.Reader, roots []string) *Client {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	return &Client{w: w, sc: sc, roots: roots}
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// request sends one request and returns its result; notifications in between are skipped.
func (c *Client) request(method string, params map[string]any) (json.RawMessage, error) {
	c.id++
	// the client declares what it could answer, so that tools reach the point of asking and gomcp call can say what they need
	meta := map[string]any{metaVersion: specVersion, metaCaps: map[string]any{
		"roots": map[string]any{}, "sampling": map[string]any{}, "elicitation": map[string]any{"form": map[string]any{}, "url": map[string]any{}},
	}}
	p := map[string]any{"_meta": meta}
	maps.Copy(p, params)
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": p})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	if _, err := c.w.Write(append(raw, '\n')); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	want := []byte(strconv.Itoa(c.id))
	for c.sc.Scan() {
		var m message
		if err := json.Unmarshal(c.sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("server sent something that is not JSON: %w", err)
		}
		if m.Method != "" || !bytes.Equal(m.ID, want) {
			continue
		}
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	}
	if err := c.sc.Err(); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return nil, errors.New("the server closed the connection")
}

// Tools lists all tools, following the pages.
func (c *Client) Tools() ([]Tool, error) {
	var all []Tool
	params := map[string]any{}
	for range 1000 {
		raw, err := c.request("tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode tools/list: %w", err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		params = map[string]any{"cursor": page.NextCursor}
	}
	return nil, errors.New("tools/list does not end")
}

type inputRequired struct {
	ResultType    string `json:"resultType"`
	InputRequests map[string]struct {
		Method string `json:"method"`
		Params struct {
			Message string `json:"message"`
		} `json:"params"`
	} `json:"inputRequests"`
	RequestState string `json:"requestState"`
}

// Call calls a tool and returns the raw result. A result with isError is returned together with a *ToolError.
func (c *Client) Call(name string, args json.RawMessage) (json.RawMessage, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 {
		arguments = args
	}
	params := map[string]any{"name": name, "arguments": arguments}
	for range maxRounds {
		raw, err := c.request("tools/call", params)
		if err != nil {
			return nil, err
		}
		var in inputRequired
		if json.Unmarshal(raw, &in) != nil || in.ResultType != "input_required" {
			return raw, toolError(raw)
		}
		answers := map[string]any{}
		var needs []string
		for key, r := range in.InputRequests {
			switch {
			case r.Method == "roots/list" && len(c.roots) > 0:
				answers[key] = map[string]any{"roots": c.rootList()}
			case r.Method == "roots/list":
				needs = append(needs, "roots, pass --root <dir>")
			case r.Method == "elicitation/create":
				needs = append(needs, "elicitation: "+r.Params.Message)
			case r.Method == "sampling/createMessage":
				needs = append(needs, "sampling")
			default:
				needs = append(needs, r.Method)
			}
		}
		if len(needs) > 0 {
			slices.Sort(needs)
			return nil, &InputError{Needs: needs}
		}
		params = map[string]any{"name": name, "arguments": arguments, "inputResponses": answers}
		if in.RequestState != "" {
			params["requestState"] = in.RequestState
		}
	}
	return nil, errors.New("the tool keeps asking for input")
}

func (c *Client) rootList() []map[string]string {
	out := make([]map[string]string, 0, len(c.roots))
	for _, dir := range c.roots {
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		p := filepath.ToSlash(abs)
		if !strings.HasPrefix(p, "/") { // C:/dir becomes /C:/dir
			p = "/" + p
		}
		out = append(out, map[string]string{"uri": (&url.URL{Scheme: "file", Path: p}).String()})
	}
	return out
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Text returns the text content blocks of a tool result, one per line.
func Text(raw json.RawMessage) string {
	var r toolResult
	_ = json.Unmarshal(raw, &r)
	var lines []string
	for _, b := range r.Content {
		if b.Type == "text" {
			lines = append(lines, b.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func toolError(raw json.RawMessage) error {
	var r toolResult
	if json.Unmarshal(raw, &r) == nil && r.IsError {
		return &ToolError{Message: Text(raw)}
	}
	return nil
}

// Indent pretty-prints raw JSON.
func Indent(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// FormatTools renders the tools as a table of name and first description line, followed by the input
// properties of each tool.
func FormatTools(tools []Tool) string {
	width := 0
	for _, t := range tools {
		width = max(width, len(t.Name))
	}
	var b strings.Builder
	for _, t := range tools {
		first, _, _ := strings.Cut(strings.TrimSpace(t.Description), "\n")
		fmt.Fprintf(&b, "%-*s  %s\n", width, t.Name, first)
		required := t.InputSchema.Required
		var optional []string
		for name := range t.InputSchema.Properties {
			if !slices.Contains(required, name) {
				optional = append(optional, name)
			}
		}
		slices.Sort(optional)
		if len(required) > 0 {
			fmt.Fprintf(&b, "    required: %s\n", strings.Join(required, ", "))
		}
		if len(optional) > 0 {
			fmt.Fprintf(&b, "    optional: %s\n", strings.Join(optional, ", "))
		}
	}
	return b.String()
}
