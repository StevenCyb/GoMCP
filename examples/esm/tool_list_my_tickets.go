package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type ListOwnTicketsFilter struct {
	ID     *string `json:"id" desc:"Only the ticket with this id."`
	Status *string `json:"status" desc:"Only tickets in this status." enum:"pending_approval,rejected,pending_resolve,waiting_for_owner,resolved"`
}

// @MCP.Tool("ListOwnTickets", title="List my tickets", description="List own tickets, optionally filtered by id or status.")
// @MCP.Hint(readOnly)
// @MCP.AuthScopes("esm:user")
func ListOwnTickets(ctx mcp.Context, in ListOwnTicketsFilter) ([]Ticket, error) {
	// Only the caller's own tickets: the owner comes from the token, never from the arguments.
	list, err := ticketService.ListOwn(ctx, ctx.Principal().Subject, in.ID, in.Status)
	return list, toolErr(err)
}
