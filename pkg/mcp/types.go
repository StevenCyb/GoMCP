package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
)

// Empty is the input or output of handlers without arguments or result.
type Empty struct{}

type Role string

const RoleUser Role = "user"

// Action is the user's answer to an elicitation.
type Action string

const (
	Accept  Action = "accept"
	Decline Action = "decline"
	Cancel  Action = "cancel"
)

type Icon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

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

func Text(s string) Content           { return Content{Type: "text", Text: s} }
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

type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type Contents []ResourceContents

// TextContent returns text for the requested resource; URI and MIME type are filled in by the server.
func TextContent(text string) ResourceContents { return ResourceContents{Text: text} }

func JSONContents(v any) (Contents, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Contents{{Text: string(b), MIMEType: "application/json"}}, nil
}

// ---- prompts

type Message struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
}

type Messages []Message

func UserText(s string) Message { return Message{Role: RoleUser, Content: Text(s)} }
func User(c Content) Message    { return Message{Role: RoleUser, Content: c} }

// Completion is the result of argument completion.
type Completion struct {
	Values  []string `json:"values"`
	Total   int      `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

// ---- pagination

type PageReq struct {
	Cursor string
	Size   int
}

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

type ResourceRef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ---- client features

type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type ModelPrefs struct {
	Hints        []string
	Cost         float64
	Speed        float64
	Intelligence float64
}

type SampleMsg = Message

func SampleText(role Role, text string) SampleMsg { return SampleMsg{Role: role, Content: Text(text)} }

type SampleTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

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

type SampleRes struct {
	Role       Role
	Content    Content
	Text       string
	Model      string
	StopReason string
}
