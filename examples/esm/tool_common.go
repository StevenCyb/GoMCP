package api

import (
	"errors"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type ConfirmUpdate struct {
	Confirm bool `json:"confirm" desc:"Apply this update?" default:"false"`
}

// confirmUpdate shows the draft and returns nil only when the user confirmed it.
// It returns mcp.ErrInputRequired on the first run, which the handler passes on.
func confirmUpdate(ctx mcp.Context, draft string) error {
	ans, action, err := mcp.Elicit[ConfirmUpdate](ctx, draft)
	if err != nil {
		return err
	}
	if action != mcp.Accept || !ans.Confirm {
		return mcp.ToolError("the user did not confirm, nothing was changed")
	}
	return nil
}

// toolErr turns the refusals of the ticket rules into errors the model sees and can act on.
// Anything else is passed on as it is.
func toolErr(err error) error {
	var rule RuleError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &rule):
		return mcp.ToolError(string(rule))
	case errors.Is(err, ErrNotFound):
		return mcp.ToolError(ErrNotFound.Error())
	case errors.Is(err, ErrConflict):
		return mcp.ToolError("the ticket was changed in the meantime, please try again")
	}
	return err
}
