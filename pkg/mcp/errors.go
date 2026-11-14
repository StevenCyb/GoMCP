package mcp

import (
	"errors"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

// Error is a JSON-RPC error. Returned from a handler it becomes a protocol error,
// as opposed to ToolError, which becomes a tool result with isError: true.
type Error = rpc.Error

var (
	// ErrNotFound is returned by resource handlers for unknown resources.
	ErrNotFound = &Error{Code: rpc.CodeInvalidParams, Message: "resource not found"}
	// ErrCapability is returned when the client lacks a capability the call needs.
	ErrCapability = errors.New("client lacks the required capability")
	// ErrInputRequired is returned by Sample, Roots, Elicit and ElicitURL when the client has to
	// answer first. A handler returns it unchanged; the server then asks the client and runs the
	// handler again with the answers. Everything before such a call therefore runs once per round.
	ErrInputRequired = errors.New("mcp: input from the client is required")
)

// ErrInvalidParams builds an invalid params protocol error.
func ErrInvalidParams(msg string) *Error { return rpc.InvalidParams(msg) }

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

// ToolError marks an execution error: the tool result gets isError: true and the LLM sees the message.
func ToolError(msg string) error { return &toolError{msg: msg} }
