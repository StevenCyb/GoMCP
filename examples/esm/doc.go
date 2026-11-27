// Package api is the ESM (Enterprise Service Management) example: tickets that a reviewer
// approves and ops process, over MCP with Microsoft Entra ID authentication.
//
// @MCP.Server("esm", title="Enterprise Service Management", version="0.1.0", description="Tickets with approval and processing.")
// @MCP.Instructions("Tickets are requests or incidents. Create one with CreateTicket: a reviewer must approve it before ops work on it. ListOwnTickets shows your tickets and their status; when a ticket is waiting_for_owner, ops asked you something: answer with UpdateMyTicket. Reviewers use ListPendingReviews and ReviewTicket, ops use UpdateTicket. Every change is shown to the user for confirmation first.")
package api
