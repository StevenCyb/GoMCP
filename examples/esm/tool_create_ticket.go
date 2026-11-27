package api

import (
	"fmt"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type NewTicket struct {
	Description string `json:"description" desc:"Ticket description."`
	Type        string `json:"type" desc:"Type of ticket." enum:"request,incident"`
}

type ConfirmTicket struct {
	Confirm bool `json:"confirm" desc:"Create this ticket?" default:"false"`
}

// @MCP.Tool("CreateTicket", title="Create ticket", description="Create a new ticket. The user sees a draft and confirms it first.")
// @MCP.Requires(elicitation)
// @MCP.AuthScopes("esm:user")
func CreateTicket(ctx mcp.Context, in NewTicket) (Ticket, error) {
	d, err := NewDraft(in.Type, in.Description)
	if err != nil {
		return Ticket{}, toolErr(err)
	}

	// Ask first, act after: the handler runs again once the user has answered.
	draft := fmt.Sprintf("Create this ticket?\n\nType: %s\nDescription: %s", d.Type, d.Text)
	ans, action, err := mcp.Elicit[ConfirmTicket](ctx, draft)
	if err != nil {
		return Ticket{}, err // mcp.ErrInputRequired on the first run
	}
	if action != mcp.Accept || !ans.Confirm {
		return Ticket{}, mcp.ToolError("the user did not confirm, no ticket was created")
	}

	t, err := ticketService.Create(ctx, ctx.Principal().Subject, d)
	return t, toolErr(err)
}
