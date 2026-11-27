package api

import (
	"fmt"
	"testing"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
)

func TestToolErr(t *testing.T) {
	t.Parallel()
	assert.NoError(t, toolErr(nil))

	// refusals and known store errors become errors the model sees
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"rule":              {RuleError("ticket T-1 is resolved and cannot be updated"), "ticket T-1 is resolved and cannot be updated"},
		"wrapped rule":      {fmt.Errorf("plan: %w", RuleError("not now")), "not now"},
		"not found":         {ErrNotFound, "ticket not found"},
		"wrapped not found": {fmt.Errorf("get: %w", ErrNotFound), "ticket not found"},
		"conflict":          {ErrConflict, "the ticket was changed in the meantime, please try again"},
		"wrapped conflict":  {fmt.Errorf("commit: %w", ErrConflict), "the ticket was changed in the meantime, please try again"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mcp.ToolError(tc.want), toolErr(tc.err))
		})
	}

	// anything else is passed on untouched, so that it is not shown to the model as a tool error
	assert.Same(t, mcp.ErrInputRequired, toolErr(mcp.ErrInputRequired))
	assert.ErrorIs(t, toolErr(assert.AnError), assert.AnError)
	assert.NotEqual(t, mcp.ToolError(assert.AnError.Error()), toolErr(assert.AnError))
}
