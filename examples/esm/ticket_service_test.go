package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The rules of the ticket life cycle, tested on their own: no server, no tokens, no confirmation
// form. Every test has its own service and store mock, so they run in parallel.

const (
	owner    = "owner-oid"
	reviewer = "reviewer-oid"
	other    = "other-oid"
)

func newService(t *testing.T) (*TicketService, *mockRepository) {
	t.Helper()
	ms := &mockRepository{}
	return NewTicketService(ms), ms
}

// stored makes the store return the ticket for its id.
func (m *mockRepository) stored(t Ticket) *mockRepository {
	m.On("Get", mock.Anything, t.ID).Return(t, nil)
	return m
}

func isRule(t *testing.T, err error, contains string) {
	t.Helper()
	var rule RuleError
	require.ErrorAs(t, err, &rule, "a refusal of the rules, not a failure")
	assert.Contains(t, rule.Error(), contains)
}

// ---- the state machine

var allStatuses = []TicketStatus{TicketStatusPendingApproval, TicketStatusRejected, TicketStatusPendingResolve, TicketStatusWaitingForOwner, TicketStatusResolved}
var allEvents = []event{evOwnerNote, evApprove, evReject, evOpsNote, evOpsAsk, evOpsResolve}

func TestStateMachine(t *testing.T) {
	t.Parallel()
	// the complete machine: every status and event not listed here is refused
	allowed := map[TicketStatus]map[event]TicketStatus{
		TicketStatusPendingApproval: {
			evOwnerNote: TicketStatusPendingApproval, evApprove: TicketStatusPendingResolve, evReject: TicketStatusRejected,
		},
		TicketStatusPendingResolve: {
			evOwnerNote: TicketStatusPendingResolve, evOpsNote: TicketStatusPendingResolve,
			evOpsAsk: TicketStatusWaitingForOwner, evOpsResolve: TicketStatusResolved,
		},
		TicketStatusWaitingForOwner: {
			evOwnerNote: TicketStatusPendingResolve, evOpsNote: TicketStatusPendingResolve,
			evOpsAsk: TicketStatusWaitingForOwner, evOpsResolve: TicketStatusResolved,
		},
	}
	for _, from := range allStatuses {
		for _, ev := range allEvents {
			t.Run(string(from)+" / "+string(ev), func(t *testing.T) {
				t.Parallel()
				p, err := plan(Ticket{ID: "T-1", CurrentStatus: from}, ev, "text")
				if want, ok := allowed[from][ev]; ok {
					require.NoError(t, err)
					assert.Equal(t, Plan{ID: "T-1", From: from, To: want, Text: "text"}, p)
				} else {
					var rule RuleError
					assert.ErrorAs(t, err, &rule)
				}
			})
		}
	}
}

func TestFinalStatusesHaveNoWayOut(t *testing.T) {
	t.Parallel()
	for _, s := range allStatuses {
		_, hasMoves := transitions[s]
		assert.Equal(t, !s.Closed(), hasMoves, s)
	}
}

func TestRefusalsSayWhatIsWrong(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from TicketStatus
		ev   event
		want string
	}{
		{TicketStatusPendingApproval, evOpsResolve, "not approved yet"},
		{TicketStatusPendingApproval, evOpsAsk, "not approved yet"},
		{TicketStatusResolved, evOwnerNote, "resolved and cannot be updated"},
		{TicketStatusRejected, evOpsNote, "rejected and cannot be updated"},
		{TicketStatusPendingResolve, evApprove, "does not wait for a review"},
		{TicketStatusResolved, evReject, "does not wait for a review"},
	}
	for _, tc := range cases {
		_, err := plan(Ticket{ID: "T-1", CurrentStatus: tc.from}, tc.ev, "x")
		isRule(t, err, tc.want)
		isRule(t, err, "T-1")
	}
}

// ---- new tickets

func TestNewDraft(t *testing.T) {
	t.Parallel()
	d, err := NewDraft("incident", "  VPN is down\n")
	require.NoError(t, err)
	assert.Equal(t, Draft{Type: TicketTypeIncident, Text: "VPN is down"}, d)

	for name, tc := range map[string]struct{ typ, text, want string }{
		"empty description": {"request", "", "description"},
		"blank description": {"request", " \t\n", "description"},
		"unknown type":      {"complaint", "x", "unknown ticket type"},
		"wrong case":        {"INCIDENT", "x", "unknown ticket type"},
		"no type":           {"", "x", "unknown ticket type"},
		"description first": {"complaint", "", "description"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewDraft(tc.typ, tc.text)
			isRule(t, err, tc.want)
		})
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	svc, ms := newService(t)
	ms.On("Create", mock.Anything, mock.MatchedBy(func(tk Ticket) bool {
		return tk.ID == "" && tk.OwnerOid == owner && tk.TicketType == TicketTypeRequest && tk.Text == "laptop" &&
			tk.CurrentStatus == TicketStatusPendingApproval && tk.Journals != nil && len(tk.Journals) == 0 &&
			time.Since(tk.Time) < time.Minute
	})).Return(Ticket{ID: "T-0001"}, nil)

	got, err := svc.Create(context.Background(), owner, Draft{Type: TicketTypeRequest, Text: "laptop"})

	require.NoError(t, err)
	assert.Equal(t, "T-0001", got.ID)
	ms.AssertExpectations(t)
}

func TestCreateStoreError(t *testing.T) {
	t.Parallel()
	svc, ms := newService(t)
	ms.On("Create", mock.Anything, mock.Anything).Return(Ticket{}, assert.AnError)

	_, err := svc.Create(context.Background(), owner, Draft{Type: TicketTypeRequest, Text: "x"})

	assert.ErrorIs(t, err, assert.AnError)
}

// ---- the owner

func TestPlanOwnerUpdate(t *testing.T) {
	t.Parallel()
	for from, to := range map[TicketStatus]TicketStatus{
		TicketStatusPendingApproval: TicketStatusPendingApproval,
		TicketStatusPendingResolve:  TicketStatusPendingResolve,
		TicketStatusWaitingForOwner: TicketStatusPendingResolve,
	} {
		t.Run(string(from), func(t *testing.T) {
			t.Parallel()
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", OwnerOid: owner, CurrentStatus: from})

			p, err := svc.PlanOwnerUpdate(context.Background(), owner, " T-1 ", " the answer\n")

			require.NoError(t, err)
			assert.Equal(t, Plan{ID: "T-1", From: from, To: to, Text: "the answer"}, p, "id and text are trimmed")
		})
	}
}

func TestPlanOwnerUpdateRefuses(t *testing.T) {
	t.Parallel()
	t.Run("closed ticket", func(t *testing.T) {
		t.Parallel()
		for _, s := range []TicketStatus{TicketStatusResolved, TicketStatusRejected} {
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", OwnerOid: owner, CurrentStatus: s})
			_, err := svc.PlanOwnerUpdate(context.Background(), owner, "T-1", "x")
			isRule(t, err, "cannot be updated")
		}
	})
	t.Run("empty message, the store is not asked", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		for _, text := range []string{"", "  \n"} {
			_, err := svc.PlanOwnerUpdate(context.Background(), owner, "T-1", text)
			isRule(t, err, "message")
		}
		never(t, ms, "Get")
	})
	t.Run("somebody else's ticket looks like a missing one", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		ms.stored(Ticket{ID: "T-1", OwnerOid: other, CurrentStatus: TicketStatusPendingResolve})
		ms.On("Get", mock.Anything, "T-2").Return(Ticket{}, ErrNotFound)

		_, theirs := svc.PlanOwnerUpdate(context.Background(), owner, "T-1", "x")
		_, missing := svc.PlanOwnerUpdate(context.Background(), owner, "T-2", "x")

		assert.ErrorIs(t, theirs, ErrNotFound)
		assert.ErrorIs(t, missing, ErrNotFound)
	})
	t.Run("store error", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		ms.On("Get", mock.Anything, "T-1").Return(Ticket{}, assert.AnError)
		_, err := svc.PlanOwnerUpdate(context.Background(), owner, "T-1", "x")
		assert.ErrorIs(t, err, assert.AnError)
	})
}

// ---- ops

func TestPlanOpsUpdate(t *testing.T) {
	t.Parallel()
	for _, from := range []TicketStatus{TicketStatusPendingResolve, TicketStatusWaitingForOwner} {
		for _, to := range []TicketStatus{TicketStatusPendingResolve, TicketStatusWaitingForOwner, TicketStatusResolved} {
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				t.Parallel()
				svc, ms := newService(t)
				ms.stored(Ticket{ID: "T-1", OwnerOid: other, CurrentStatus: from}) // any owner

				p, err := svc.PlanOpsUpdate(context.Background(), "T-1", string(to), " note ")

				require.NoError(t, err)
				assert.Equal(t, Plan{ID: "T-1", From: from, To: to, Text: "note"}, p)
			})
		}
	}
}

func TestPlanOpsUpdateRefuses(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"pending_approval", "rejected", "open", "pending", "closed", "RESOLVED", ""} {
		t.Run("status "+status, func(t *testing.T) {
			t.Parallel()
			svc, ms := newService(t)
			_, err := svc.PlanOpsUpdate(context.Background(), "T-1", status, "x")
			isRule(t, err, "is not allowed")
			never(t, ms, "Get")
		})
	}
	t.Run("not approved yet", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		ms.stored(Ticket{ID: "T-1", CurrentStatus: TicketStatusPendingApproval})
		_, err := svc.PlanOpsUpdate(context.Background(), "T-1", "resolved", "x")
		isRule(t, err, "not approved yet")
	})
	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		for _, s := range []TicketStatus{TicketStatusResolved, TicketStatusRejected} {
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", CurrentStatus: s})
			_, err := svc.PlanOpsUpdate(context.Background(), "T-1", "resolved", "x")
			isRule(t, err, "cannot be updated")
		}
	})
	t.Run("empty message", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		_, err := svc.PlanOpsUpdate(context.Background(), "T-1", "resolved", " ")
		isRule(t, err, "message")
		never(t, ms, "Get")
	})
	t.Run("missing ticket", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		ms.On("Get", mock.Anything, "T-1").Return(Ticket{}, ErrNotFound)
		_, err := svc.PlanOpsUpdate(context.Background(), "T-1", "resolved", "x")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

// ---- reviewers

func TestPlanReview(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		approve bool
		to      TicketStatus
	}{
		"approve": {true, TicketStatusPendingResolve},
		"reject":  {false, TicketStatusRejected},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", OwnerOid: owner, CurrentStatus: TicketStatusPendingApproval})

			p, err := svc.PlanReview(context.Background(), reviewer, " T-1", tc.approve, " fits the policy ")

			require.NoError(t, err)
			assert.Equal(t, Plan{ID: "T-1", From: TicketStatusPendingApproval, To: tc.to, Text: "fits the policy"}, p)
		})
	}
}

func TestPlanReviewRefuses(t *testing.T) {
	t.Parallel()
	t.Run("own ticket, whatever the status", func(t *testing.T) {
		t.Parallel()
		for _, s := range allStatuses {
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", OwnerOid: reviewer, CurrentStatus: s})
			for _, approve := range []bool{true, false} {
				_, err := svc.PlanReview(context.Background(), reviewer, "T-1", approve, "ok")
				isRule(t, err, "your own ticket")
			}
		}
	})
	t.Run("not waiting for a review", func(t *testing.T) {
		t.Parallel()
		for _, s := range []TicketStatus{TicketStatusPendingResolve, TicketStatusWaitingForOwner, TicketStatusResolved, TicketStatusRejected} {
			svc, ms := newService(t)
			ms.stored(Ticket{ID: "T-1", OwnerOid: owner, CurrentStatus: s})
			_, err := svc.PlanReview(context.Background(), reviewer, "T-1", true, "ok")
			isRule(t, err, "does not wait for a review")
		}
	})
	t.Run("no reason, the store is not asked", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		for _, reason := range []string{"", " \n"} {
			_, err := svc.PlanReview(context.Background(), reviewer, "T-1", true, reason)
			isRule(t, err, "reason")
		}
		never(t, ms, "Get")
	})
	t.Run("missing ticket", func(t *testing.T) {
		t.Parallel()
		svc, ms := newService(t)
		ms.On("Get", mock.Anything, "T-1").Return(Ticket{}, ErrNotFound)
		_, err := svc.PlanReview(context.Background(), reviewer, "T-1", true, "ok")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

// ---- writing

func TestCommit(t *testing.T) {
	t.Parallel()
	svc, ms := newService(t)
	ms.On("AppendJournal", mock.Anything, "T-1", TicketStatusWaitingForOwner, mock.MatchedBy(func(j Journal) bool {
		return j.Status == TicketStatusPendingResolve && j.AuthorOid == owner && j.Text == "the answer" && time.Since(j.Time) < time.Minute
	})).Return(Ticket{}, nil)

	err := svc.Commit(context.Background(), Plan{ID: "T-1", From: TicketStatusWaitingForOwner, To: TicketStatusPendingResolve, Text: "the answer"}, owner)

	require.NoError(t, err)
	ms.AssertExpectations(t) // written against the status the plan was made for
}

func TestCommitErrorsReachTheCaller(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]error{"conflict": ErrConflict, "deleted": ErrNotFound, "failure": assert.AnError} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, ms := newService(t)
			ms.On("AppendJournal", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(Ticket{}, want)

			err := svc.Commit(context.Background(), Plan{ID: "T-1"}, owner)

			assert.True(t, errors.Is(err, want))
		})
	}
}

// ---- lists

func TestListOwn(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	cases := map[string]struct {
		id, status *string
		want       TicketFilter
	}{
		"no filter":       {nil, nil, TicketFilter{}},
		"blank id":        {str("  "), nil, TicketFilter{}},
		"id is trimmed":   {str(" T-1 "), nil, TicketFilter{ID: str("T-1")}},
		"status":          {nil, str("waiting_for_owner"), TicketFilter{Status: new(TicketStatusWaitingForOwner)}},
		"id and status":   {str("T-1"), str("resolved"), TicketFilter{ID: str("T-1"), Status: new(TicketStatusResolved)}},
		"pending approve": {nil, str("pending_approval"), TicketFilter{Status: new(TicketStatusPendingApproval)}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, ms := newService(t)
			ms.On("ListByOwner", mock.Anything, owner, tc.want).Return([]Ticket{{ID: "T-1"}}, nil)

			got, err := svc.ListOwn(context.Background(), owner, tc.id, tc.status)

			require.NoError(t, err)
			assert.Len(t, got, 1)
			ms.AssertExpectations(t)
		})
	}
}

func TestListOwnUnknownStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"open", "pending", "closed", "RESOLVED", "", "invalid"} {
		svc, ms := newService(t)
		_, err := svc.ListOwn(context.Background(), owner, nil, &status)
		isRule(t, err, "unknown status")
		never(t, ms, "ListByOwner")
	}
}

func TestListPending(t *testing.T) {
	t.Parallel()
	svc, ms := newService(t)
	ms.On("ListByStatus", mock.Anything, TicketStatusPendingApproval, reviewer).Return([]Ticket{{ID: "T-2"}}, nil) // not the reviewer's own

	got, err := svc.ListPending(context.Background(), reviewer)

	require.NoError(t, err)
	assert.Equal(t, []Ticket{{ID: "T-2"}}, got)
	ms.AssertExpectations(t)
}
