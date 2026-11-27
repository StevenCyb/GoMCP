// Package project is the model of an annotated Go module: what the parser finds and
// what the generator turns into code.
package project

import "fmt"

// Pos is a source position, with the file relative to the module directory.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.Col > 0 {
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// Project is everything the generator needs.
type Project struct {
	Module Module
	Server Server

	Transport Transport
	Auth      []Auth
	// Middleware wraps every feature, in declaration order.
	Middleware []FuncRef
	Setup      []Setup
	Teardown   []FuncRef
	Assets     []Assets
	// Handlers are plain HTTP handlers served next to the MCP endpoint (HTTP only).
	Handlers []HTTPHandler

	Tools       []Tool
	Resources   []Resource
	Lists       []ResourceList
	Prompts     []Prompt
	Completions []Completion
}

// HTTPHandler is a plain HTTP handler registered on the mux of the server.
type HTTPHandler struct {
	FuncRef

	Pattern string // http.ServeMux pattern, for example "POST /hook"
}

// Setup is a startup hook.
type Setup struct {
	FuncRef

	ReturnsError bool
}

// Module is the Go module of the project.
type Module struct {
	Path string
	Dir  string
}

// FuncRef points to a function (or variable) of the user's code.
type FuncRef struct {
	Pkg        string // package name
	ImportPath string
	Name       string
	Pos        Pos
}

// Icon is an icon of the server or of a feature.
type Icon struct {
	Src   string
	MIME  string
	Sizes []string
	Theme string
}

// KV is one entry of _meta; Value is a string, float64 or bool.
type KV struct {
	Key   string
	Value any
}

// Server is the identity of the server.
type Server struct {
	Name         string
	Title        string
	Version      string
	Description  string
	Website      string
	BaseURL      string
	Instructions string
	Icons        []Icon
}

// Transport holds the transport settings.
type Transport struct {
	Endpoint       string
	AllowedOrigins []string
	KeepAlive      string
	PageSize       int
	CacheTTL       string // ttlMs hint of discover, list and read results, empty = none
	CacheScope     string // public or private
}

// ScopeDef is an OAuth scope.
type ScopeDef struct {
	Name    string
	Default bool
}

// Auth is an authentication scheme with its verifier.
type Auth struct {
	Type                 string // bearer or oauth
	Resource             string
	AuthorizationServers []string
	Scopes               []ScopeDef
	Verify               FuncRef
}

// Assets serves an embedded file system under Prefix.
type Assets struct {
	Prefix string
	Var    FuncRef
}

// Common is shared by all features.
type Common struct {
	Func        FuncRef
	Name        string
	Title       string
	Description string
	Icons       []Icon
	Meta        []KV
	Scopes      [][]string // groups ANDed, scopes within a group ORed
	Use         []FuncRef
	// Flag is the feature flag name; the feature is not registered if it is disabled at start.
	Flag string
}

// Hint carries the tool annotations.
type Hint struct {
	ReadOnly, Destructive, Idempotent, OpenWorld bool
}

// Task configures task support of a tool.
type Task struct {
	Mode string // forbidden, optional, required; empty = not set
	TTL  string
	Poll string
}

// Tool is a tool handler.
type Tool struct {
	Common

	Hint     Hint
	Task     Task
	Requires []string // client capabilities: sampling, roots, elicitation, elicitation:url
}

// Annotations are resource annotations.
type Annotations struct {
	Audience     []string
	Priority     *float64
	LastModified string
}

// Resource is a resource or resource template handler.
type Resource struct {
	Common

	URI          string // Name holds the resource name
	MIME         string
	Subscribable bool
	Annotations  *Annotations
	Template     bool
}

// ResourceList lists the concrete resources of a template.
type ResourceList struct {
	Template string
	Func     FuncRef
}

// Prompt is a prompt handler.
type Prompt struct {
	Common
}

// Completion provides completion for a prompt argument or a resource template variable.
type Completion struct {
	Prompt   string // prompt name, or
	Template string // resource template URI
	Arg      string
	Func     *FuncRef // dynamic completion, or
	Values   []string // static values
	Pos      Pos
}
