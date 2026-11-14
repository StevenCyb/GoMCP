package mcp

import (
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrors(t *testing.T) {
	assert.Equal(t, rpc.CodeInvalidParams, ErrNotFound.Code, "a missing resource is an invalid params error")
	assert.Equal(t, rpc.CodeInvalidParams, ErrInvalidParams("bad").Code)
	require.Error(t, ErrCapability)
	require.Error(t, ErrInputRequired)
}

func TestToolError(t *testing.T) {
	var te *toolError
	require.ErrorAs(t, ToolError("boom"), &te)
	assert.Equal(t, "boom", te.Error())
}
