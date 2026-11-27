package api

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RuleError is a refusal by the ticket rules. Its text is meant for the caller (and the model),
// so it says what is wrong and what would work.
type RuleError string

func (e RuleError) Error() string { return string(e) }

func refuse(format string, a ...any) error { return RuleError(fmt.Sprintf(format, a...)) }

// event is something that happens to a ticket. Who may cause it is decided by the caller
// (scopes, ownership); what it does to the ticket is decided by transitions.
type event string

const (
	evOwnerNote  event = "owner adds information or answers"
	evApprove    event = "reviewer approves"
	evReject     event = "reviewer rejects"
	evOpsNote    event = "ops add a note"
	evOpsAsk     event = "ops ask the owner"
	evOpsResolve event = "ops resolve"
)

// transitions is the state machine: status, then event, gives the next status.
// A status without an entry is final.
//
//	pending_approval -> rejected | pending_resolve
//	pending_resolve <-> waiting_for_owner, pending_resolve -> resolved
var transitions = map[TicketStatus]map[event]TicketStatus{
	TicketStatusPendingApproval: {
		evOwnerNote: TicketStatusPendingApproval,
		evApprove:   TicketStatusPendingResolve,
		evReject:    TicketStatusRejected,
	},
	TicketStatusPendingResolve: {
		evOwnerNote:  TicketStatusPendingResolve,
		evOpsNote:    TicketStatusPendingResolve,
		evOpsAsk:     TicketStatusWaitingForOwner,
		evOpsResolve: TicketStatusResolved,
	},
	TicketStatusWaitingForOwner: {
		evOwnerNote:  TicketStatusPendingResolve, // the answer hands the ticket back to ops
		evOpsNote:    TicketStatusPendingResolve,
		evOpsAsk:     TicketStatusWaitingForOwner,
		evOpsResolve: TicketStatusResolved,
	},
}

// opsEvents maps the status ops ask for to the event.
var opsEvents = map[TicketStatus]event{
	TicketStatusPendingResolve:  evOpsNote,
	TicketStatusWaitingForOwner: evOpsAsk,
	TicketStatusResolved:        evOpsResolve,
}

// Plan is a checked change of a ticket that is ready to be written. Plans are made first and
// committed after the user confirmed, so nothing is written for a refused or declined change.
type Plan struct {
	ID       string
	From, To TicketStatus
	Text     string // the journal text, trimmed
}

// Draft is a checked new ticket.
type Draft struct {
	Type TicketType
	Text string
}

// NewDraft checks and normalizes the input of a new ticket.
func NewDraft(typ, text string) (Draft, error) {
	text, err := requireText(text, "description")
	if err != nil {
		return Draft{}, err
	}
	t := TicketTypeFrom(typ)
	if t == TicketTypeInvalid {
		return Draft{}, refuse("unknown ticket type %q, use request or incident", typ)
	}
	return Draft{Type: t, Text: text}, nil
}

// TicketService holds the rules of the ticket life cycle. It reads and writes through the
// repository, which only persists.
type TicketService struct{ repository TicketRepository }

func NewTicketService(r TicketRepository) *TicketService { return &TicketService{repository: r} }

// Create stores a new ticket of the owner. It starts in pending_approval.
func (s *TicketService) Create(ctx context.Context, owner string, d Draft) (Ticket, error) {
	return s.repository.Create(ctx, Ticket{
		Time:          time.Now().UTC(),
		OwnerOid:      owner,
		TicketType:    d.Type,
		CurrentStatus: TicketStatusPendingApproval,
		Text:          d.Text,
		Journals:      []Journal{},
	})
}

// PlanOwnerUpdate plans an answer or additional information of the owner. A ticket of somebody
// else is reported as not found.
func (s *TicketService) PlanOwnerUpdate(ctx context.Context, by, id, text string) (Plan, error) {
	text, err := requireText(text, "message")
	if err != nil {
		return Plan{}, err
	}
	t, err := s.load(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if t.OwnerOid != by {
		return Plan{}, ErrNotFound // do not reveal that the ticket exists
	}
	return plan(t, evOwnerNote, text)
}

// PlanOpsUpdate plans a change by ops to pending_resolve, waiting_for_owner or resolved.
func (s *TicketService) PlanOpsUpdate(ctx context.Context, id, status, text string) (Plan, error) {
	text, err := requireText(text, "message")
	if err != nil {
		return Plan{}, err
	}
	ev, ok := opsEvents[TicketStatusFrom(status)]
	if !ok {
		return Plan{}, refuse("status %q is not allowed, use pending_resolve, waiting_for_owner or resolved", status)
	}
	t, err := s.load(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	return plan(t, ev, text)
}

// PlanReview plans the decision of a reviewer. Nobody reviews their own ticket.
func (s *TicketService) PlanReview(ctx context.Context, by, id string, approve bool, reason string) (Plan, error) {
	reason, err := requireText(reason, "reason")
	if err != nil {
		return Plan{}, err
	}
	t, err := s.load(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if t.OwnerOid == by {
		return Plan{}, refuse("you cannot review your own ticket")
	}
	ev := evReject
	if approve {
		ev = evApprove
	}
	return plan(t, ev, reason)
}

// Commit writes a plan: it adds the journal entry and moves the ticket to the planned status.
// It fails with ErrConflict when the ticket is no longer in the status the plan was made for.
func (s *TicketService) Commit(ctx context.Context, p Plan, by string) error {
	_, err := s.repository.AppendJournal(ctx, p.ID, p.From, Journal{Status: p.To, Time: time.Now().UTC(), AuthorOid: by, Text: p.Text})
	return err
}

// ListOwn lists the tickets of one owner, optionally only one id or status.
func (s *TicketService) ListOwn(ctx context.Context, owner string, id, status *string) ([]Ticket, error) {
	var f TicketFilter
	if id != nil {
		if v := strings.TrimSpace(*id); v != "" {
			f.ID = &v
		}
	}
	if status != nil {
		st := TicketStatusFrom(*status)
		if st == TicketStatusInvalid {
			return nil, refuse("unknown status %q, use pending_approval, rejected, pending_resolve, waiting_for_owner or resolved", *status)
		}
		f.Status = &st
	}
	return s.repository.ListByOwner(ctx, owner, f)
}

// ListPending lists the tickets that wait for a review, except the reviewer's own.
func (s *TicketService) ListPending(ctx context.Context, reviewer string) ([]Ticket, error) {
	return s.repository.ListByStatus(ctx, TicketStatusPendingApproval, reviewer)
}

func (s *TicketService) load(ctx context.Context, id string) (Ticket, error) {
	return s.repository.Get(ctx, strings.TrimSpace(id))
}

func plan(t Ticket, ev event, text string) (Plan, error) {
	to, ok := transitions[t.CurrentStatus][ev]
	if !ok {
		switch {
		case ev == evApprove || ev == evReject:
			return Plan{}, refuse("ticket %s is %s and does not wait for a review", t.ID, t.CurrentStatus)
		case t.CurrentStatus == TicketStatusPendingApproval:
			return Plan{}, refuse("ticket %s is not approved yet and cannot be processed", t.ID)
		default:
			return Plan{}, refuse("ticket %s is %s and cannot be updated", t.ID, t.CurrentStatus)
		}
	}
	return Plan{ID: t.ID, From: t.CurrentStatus, To: to, Text: text}, nil
}

func requireText(s, what string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", refuse("the %s must not be empty", what)
	}
	return s, nil
}
