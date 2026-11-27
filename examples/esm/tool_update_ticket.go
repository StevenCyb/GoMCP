package api

import (
	"fmt"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type OwnTicketUpdate struct {
	ID   string `json:"id" desc:"Ticket id to update."`
	Text string `json:"message" desc:"Your answer or additional information."`
}

type TicketUpdate struct {
	ID     string `json:"id" desc:"Ticket id to update."`
	Text   string `json:"message" desc:"Follow-up question, information or the resolution for the owner."`
	Status string `json:"status" desc:"New status: waiting_for_owner asks the owner, resolved closes the ticket." enum:"pending_resolve,waiting_for_owner,resolved"`
}

// UpdateMyTicket lets the owner add information or answer a follow-up question of ops.
//
// @MCP.Tool("UpdateMyTicket", title="Update my ticket", description="Update your own ticket by appending answer/information.")
// @MCP.Requires(elicitation)
// @MCP.AuthScopes("esm:user")
func UpdateMyTicket(ctx mcp.Context, in OwnTicketUpdate) (mcp.Empty, error) {
	me := ctx.Principal().Subject
	p, err := ticketService.PlanOwnerUpdate(ctx, me, in.ID, in.Text)
	if err != nil {
		return mcp.Empty{}, toolErr(err)
	}
	if err := confirmUpdate(ctx, fmt.Sprintf("Add this to ticket %s?\n\n%s", p.ID, p.Text)); err != nil {
		return mcp.Empty{}, err
	}
	return mcp.Empty{}, toolErr(ticketService.Commit(ctx, p, me))
}

// UpdateTicket lets ops work on an approved ticket: add a note, ask the owner or resolve it.
//
// @MCP.Tool("UpdateTicket", title="Update ticket (ops)", description="Update others ticket by appending question/information.")
// @MCP.Requires(elicitation)
// @MCP.AuthScopes("esm:ops")
func UpdateTicket(ctx mcp.Context, in TicketUpdate) (mcp.Empty, error) {
	p, err := ticketService.PlanOpsUpdate(ctx, in.ID, in.Status, in.Text)
	if err != nil {
		return mcp.Empty{}, toolErr(err)
	}
	if err := confirmUpdate(ctx, fmt.Sprintf("Set ticket %s to %s?\n\n%s", p.ID, p.To, p.Text)); err != nil {
		return mcp.Empty{}, err
	}
	return mcp.Empty{}, toolErr(ticketService.Commit(ctx, p, ctx.Principal().Subject))
}
