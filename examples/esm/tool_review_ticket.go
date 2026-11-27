package api

import (
	"fmt"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

type ReviewDecision struct {
	ID     string `json:"id" desc:"Ticket id to review."`
	Result bool   `json:"result" desc:"true approves the ticket for processing, false rejects it."`
	Reason string `json:"reason" desc:"Reason for the decision, the owner sees it."`
}

// ListPendingReviews lists the tickets that wait for approval, except the caller's own.
//
// @MCP.Tool("ListPendingReviews", title="List pending reviews", description="List tickets that wait for my review.")
// @MCP.Hint(readOnly)
// @MCP.AuthScopes("esm:reviewer")
func ListPendingReviews(ctx mcp.Context, _ mcp.Empty) ([]Ticket, error) {
	list, err := ticketService.ListPending(ctx, ctx.Principal().Subject)
	return list, toolErr(err)
}

// ReviewTicket approves or rejects a ticket that waits for approval.
//
// @MCP.Tool("ReviewTicket", title="Review ticket", description="Approve or reject a ticket that waits for my review.")
// @MCP.Requires(elicitation)
// @MCP.AuthScopes("esm:reviewer")
func ReviewTicket(ctx mcp.Context, in ReviewDecision) (mcp.Empty, error) {
	me := ctx.Principal().Subject
	p, err := ticketService.PlanReview(ctx, me, in.ID, in.Result, in.Reason)
	if err != nil {
		return mcp.Empty{}, toolErr(err)
	}
	verb := "Reject"
	if in.Result {
		verb = "Approve"
	}
	if err := confirmUpdate(ctx, fmt.Sprintf("%s ticket %s?\n\n%s", verb, p.ID, p.Text)); err != nil {
		return mcp.Empty{}, err
	}
	return mcp.Empty{}, toolErr(ticketService.Commit(ctx, p, me))
}
