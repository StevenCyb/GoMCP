package api

import "time"

type TicketStatus string

const (
	TicketStatusInvalid         TicketStatus = "invalid"
	TicketStatusPendingApproval TicketStatus = "pending_approval"  // new: waits for a reviewer
	TicketStatusRejected        TicketStatus = "rejected"          // refused by the reviewer (final)
	TicketStatusPendingResolve  TicketStatus = "pending_resolve"   // approved: ops work on it
	TicketStatusWaitingForOwner TicketStatus = "waiting_for_owner" // ops asked the owner something
	TicketStatusResolved        TicketStatus = "resolved"          // done (final)
)

// Closed reports whether the ticket is final: nobody can update it anymore.
func (s TicketStatus) Closed() bool { return s == TicketStatusRejected || s == TicketStatusResolved }

func TicketStatusFrom(s string) TicketStatus {
	switch s {
	case "pending_approval":
		return TicketStatusPendingApproval
	case "rejected":
		return TicketStatusRejected
	case "pending_resolve":
		return TicketStatusPendingResolve
	case "waiting_for_owner":
		return TicketStatusWaitingForOwner
	case "resolved":
		return TicketStatusResolved
	default:
		return TicketStatusInvalid
	}
}

type TicketType string

const (
	TicketTypeInvalid  TicketType = "invalid"
	TicketTypeRequest  TicketType = "request"
	TicketTypeIncident TicketType = "incident"
)

func TicketTypeFrom(s string) TicketType {
	switch s {
	case "request":
		return TicketTypeRequest
	case "incident":
		return TicketTypeIncident
	default:
		return TicketTypeInvalid
	}
}

type Ticket struct {
	ID            string       `json:"id"`
	Time          time.Time    `json:"time"`
	OwnerOid      string       `json:"owner_oid"`
	TicketType    TicketType   `json:"type"`
	CurrentStatus TicketStatus `json:"current_status"`
	Text          string       `json:"text"`
	Journals      []Journal    `json:"journals"`
}

type Journal struct {
	Status    TicketStatus `json:"status"`
	Time      time.Time    `json:"time"`
	AuthorOid string       `json:"author_oid"`
	Text      string       `json:"text"`
}
