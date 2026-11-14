// Package rpc holds the JSON-RPC 2.0 message types used by MCP.
package rpc

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC and MCP error codes.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	// CodeHeaderMismatch and the following codes are MCP specific, -32020 to -32099 are reserved for the specification.
	CodeHeaderMismatch      = -32020
	CodeMissingCapability   = -32021
	CodeUnsupportedProtocol = -32022
)

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// ParseError reports malformed JSON.
func ParseError(msg string) *Error { return &Error{Code: CodeParse, Message: msg} }

// InvalidRequest reports a message that is valid JSON but not a valid request.
func InvalidRequest(msg string) *Error { return &Error{Code: CodeInvalidRequest, Message: msg} }

// MethodNotFound reports an unknown method.
func MethodNotFound(method string) *Error {
	return &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

// InvalidParams reports bad method parameters.
func InvalidParams(msg string) *Error { return &Error{Code: CodeInvalidParams, Message: msg} }

// Internal reports a server side failure.
func Internal(msg string) *Error { return &Error{Code: CodeInternal, Message: msg} }

// HeaderMismatch reports a missing or wrong HTTP header.
func HeaderMismatch(msg string) *Error { return &Error{Code: CodeHeaderMismatch, Message: msg} }

// MissingCapability reports that the client must declare capabilities (a ClientCapabilities object) for the request.
func MissingCapability(required any) *Error {
	return &Error{
		Code: CodeMissingCapability, Message: "Missing required client capability",
		Data: map[string]any{"requiredCapabilities": required},
	}
}

// UnsupportedVersion reports an unsupported protocol version and the supported ones.
func UnsupportedVersion(supported []string, requested string) *Error {
	return &Error{
		Code: CodeUnsupportedProtocol, Message: "Unsupported protocol version",
		Data: map[string]any{"supported": supported, "requested": requested},
	}
}

// Message is any JSON-RPC message. A message with a method and an id is a request,
// a method without an id a notification, and no method a response.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// HasID reports whether the message carries a non-null id.
func (m *Message) HasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// IsRequest reports whether the message expects a response.
func (m *Message) IsRequest() bool { return m.Method != "" && m.HasID() }

// IsNotification reports whether the message is fire and forget.
func (m *Message) IsNotification() bool { return m.Method != "" && !m.HasID() }

// NewNotification builds a notification.
func NewNotification(method string, params any) Message {
	return Message{JSONRPC: "2.0", Method: method, Params: MustJSON(params)}
}

// NewResult builds a successful response.
func NewResult(id json.RawMessage, result any) Message {
	return Message{JSONRPC: "2.0", ID: id, Result: MustJSON(result)}
}

// NewError builds an error response.
func NewError(id json.RawMessage, e *Error) Message {
	return Message{JSONRPC: "2.0", ID: id, Error: e}
}

// MustJSON marshals v and panics on failure; it is only used for values built by this package.
func MustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("rpc: marshal %T: %v", v, err))
	}
	return b
}
