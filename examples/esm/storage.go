package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

type TicketRepository interface {
	// Create stores a new ticket and returns it with its id.
	Create(ctx context.Context, t Ticket) (Ticket, error)
	// ListByOwner returns the tickets of one owner that match the filter, ordered by id.
	// There is no call that lists other people's tickets: callers can only see their own.
	ListByOwner(ctx context.Context, owner string, f TicketFilter) ([]Ticket, error)
	// ListByStatus returns the tickets in a status, except those of one owner, ordered by id.
	ListByStatus(ctx context.Context, status TicketStatus, exceptOwner string) ([]Ticket, error)
	// Get returns the ticket or ErrNotFound.
	Get(ctx context.Context, id string) (Ticket, error)
	// AppendJournal adds the entry to the ticket and makes its status the current one, in one step,
	// but only while the ticket is still in the expected status (compare and set). It returns the
	// updated ticket, ErrNotFound, or ErrConflict when the status has changed.
	AppendJournal(ctx context.Context, id string, expected TicketStatus, j Journal) (Ticket, error)
}

var (
	ErrNotFound = errors.New("ticket not found")
	ErrConflict = errors.New("ticket changed in the meantime")
)

// TicketFilter narrows a listing; nil fields match everything.
type TicketFilter struct {
	ID     *string
	Status *TicketStatus
}

var ticketService *TicketService // the handlers reach the rules through it, set once in Setup

// InMemoryRepository keeps tickets in the process: fine for the example, not for several instances.
type InMemoryRepository struct {
	mu      sync.Mutex
	next    int
	tickets map[string]Ticket
}

func (m *InMemoryRepository) Create(_ context.Context, t Ticket) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	t.ID = fmt.Sprintf("T-%04d", m.next)
	m.tickets[t.ID] = t
	return t, nil
}

func (m *InMemoryRepository) ListByOwner(_ context.Context, owner string, f TicketFilter) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Ticket{}
	for _, t := range m.tickets {
		if t.OwnerOid != owner || (f.ID != nil && t.ID != *f.ID) || (f.Status != nil && t.CurrentStatus != *f.Status) {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Ticket) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *InMemoryRepository) ListByStatus(_ context.Context, status TicketStatus, exceptOwner string) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Ticket{}
	for _, t := range m.tickets {
		if t.CurrentStatus == status && t.OwnerOid != exceptOwner {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Ticket) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *InMemoryRepository) Get(_ context.Context, id string) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, ErrNotFound
	}
	return t, nil
}

func (m *InMemoryRepository) AppendJournal(_ context.Context, id string, expected TicketStatus, j Journal) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, ErrNotFound
	}
	if t.CurrentStatus != expected {
		return Ticket{}, ErrConflict
	}
	t.Journals = append(slices.Clone(t.Journals), j)
	t.CurrentStatus = j.Status
	m.tickets[id] = t
	return t, nil
}

// @MCP.Setup
func Setup() error {
	ticketService = NewTicketService(&InMemoryRepository{tickets: map[string]Ticket{}})
	return nil
}

// @MCP.Teardown
func Teardown() {
	// _ = db.Close()
}
