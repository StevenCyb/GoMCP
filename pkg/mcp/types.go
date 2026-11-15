package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
)

// Empty is the input or output of handlers without arguments or result.
type Empty struct{}

// Role is the author of a message: the user or the assistant.
type Role string

// RoleUser is the role of messages written by the user (or the client on its behalf).
const RoleUser Role = "user"

// Action is the user's answer to an elicitation.
type Action string

// Possible Actions: Accept means the user submitted the form, Decline an explicit refusal, Cancel a dismissal without choice.
const (
	Accept  Action = "accept"
	Decline Action = "decline"
	Cancel  Action = "cancel"
)

// Icon is an image that represents a server, tool, resource or prompt; Src is a URL or data URI, Sizes like "48x48".
type Icon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

// Annotations are optional hints for clients on how to use a content block or resource: intended audience,
// priority (0 to 1, higher is more important) and last modification time (RFC 3339).
type Annotations struct {
	Audience     []Role   `json:"audience,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

// Principal is the authenticated caller.
type Principal struct {
	Subject string
	Scopes  []string
}

// ---- content blocks (tool results, prompt messages, sampling)

const typeResource = "resource"

// Content is a content block: text, image, audio, resource link or embedded resource.
type Content struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Data        string            `json:"data,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Name        string            `json:"name,omitempty"`
	Resource    *ResourceContents `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`

	embed string // EmbedResource: resolved by the server before sending
}

// Text returns a text content block.
func Text(s string) Content { return Content{Type: "text", Text: s} }

// ResourceLink returns a content block that links to a resource by URI without embedding it.
func ResourceLink(uri string) Content { return Content{Type: "resource_link", URI: uri, Name: uri} }

// EmbedResource embeds the contents of a registered resource; the server reads it when the message is built.
func EmbedResource(uri string) Content { return Content{Type: typeResource, embed: uri} }

// Result is a tool result with extra content next to the structured value.
type Result[T any] struct {
	Structured T
	Content    []Content
}

// resultValue lets the tool wrapper unpack any Result[T] without knowing T.
type resultValue interface {
	structured() any
	extra() []Content
}

func (r Result[T]) structured() any  { return r.Structured }
func (r Result[T]) extra() []Content { return r.Content }

// ---- resource contents

// ResourceContents is the content of a resource: Text for text, Blob (base64) for binary data.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// Contents is the result of a resource read handler; a resource can return several parts.
type Contents []ResourceContents

// TextContent returns text for the requested resource; URI and MIME type are filled in by the server.
func TextContent(text string) ResourceContents { return ResourceContents{Text: text} }

// JSONContents marshals v and returns it as application/json resource contents.
func JSONContents(v any) (Contents, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Contents{{Text: string(b), MIMEType: "application/json"}}, nil
}

// ---- prompts

// Message is a prompt or sampling message: a role and one content block.
type Message struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
}

// Messages is the result of a prompt handler.
type Messages []Message

// UserText returns a user message with the given text.
func UserText(s string) Message { return Message{Role: RoleUser, Content: Text(s)} }

// User returns a user message with the given content block, e.g. from EmbedResource.
func User(c Content) Message { return Message{Role: RoleUser, Content: c} }

// Completion is the result of argument completion.
type Completion struct {
	Values  []string `json:"values"`
	Total   int      `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

// ---- pagination

// PageReq is a pagination request: an opaque Cursor from a previous Page (empty for the first page) and the
// maximum page Size (0 means no limit).
type PageReq struct {
	Cursor string
	Size   int
}

// Page is one page of items; Next is the cursor of the following page and empty on the last page.
type Page[T any] struct {
	Items []T
	Next  string
}

// Paginate slices items according to the cursor (an opaque offset) and page size.
func Paginate[T any](items []T, p PageReq) Page[T] {
	start := 0
	if p.Cursor != "" {
		if b, err := base64.RawURLEncoding.DecodeString(p.Cursor); err == nil {
			if n, err := strconv.Atoi(string(b)); err == nil && n >= 0 {
				start = n
			}
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := len(items)
	page := Page[T]{}
	if p.Size > 0 && start+p.Size < end {
		end = start + p.Size
		page.Next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	page.Items = items[start:end]
	return page
}

// ResourceRef describes a concrete resource in a listing.
type ResourceRef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ---- client features

// Root is a location (usually a file:// URI) the client lets the server work in.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// ModelPrefs are the server's model preferences for sampling: Hints are model name substrings in order of
// preference, Cost, Speed and Intelligence are priorities from 0 to 1.
type ModelPrefs struct {
	Hints        []string
	Cost         float64
	Speed        float64
	Intelligence float64
}

// SampleMsg is a message of a sampling request.
type SampleMsg = Message

// SampleText returns a sampling message with the given role and text.
func SampleText(role Role, text string) SampleMsg { return SampleMsg{Role: role, Content: Text(text)} }

// SampleTool is a tool the client's model may call during sampling; needs the CapSamplingTools capability.
type SampleTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// SampleReq is a sampling request: ask the client's LLM for a completion; needs the CapSampling capability.
type SampleReq struct {
	System        string
	Messages      []SampleMsg
	MaxTokens     int
	Prefs         ModelPrefs
	Tools         []SampleTool
	ToolChoice    string // auto, required, none
	Temperature   *float64
	StopSequences []string
}

// SampleRes is the client's sampling answer; Text is the text of Content when it is a text block.
type SampleRes struct {
	Role       Role
	Content    Content
	Text       string
	Model      string
	StopReason string
}
