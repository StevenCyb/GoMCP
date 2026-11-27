package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	ticketID  = "T-0001"
	notFound  = "ticket not found"
	oneOf     = "is not one of pending_resolve, waiting_for_owner, resolved"
	cantApply = "cannot be updated"
	noConfirm = "nothing was changed"
)

func ticketOf(owner string, status TicketStatus) Ticket {
	return Ticket{ID: ticketID, OwnerOid: owner, TicketType: TicketTypeRequest, CurrentStatus: status, Text: "t", Journals: []Journal{}}
}

// entry matches the journal entry a tool appends.
func entry(status TicketStatus, author, text string) any {
	return mock.MatchedBy(func(j Journal) bool {
		return j.Status == status && j.AuthorOid == author && j.Text == text && time.Since(j.Time) < time.Minute
	})
}

// never checks that the store was not called. The arity must match the method's, or the check passes for nothing.
func never(t *testing.T, ms *mockRepository, methods ...string) {
	t.Helper()
	arity := map[string]int{"Create": 2, "Get": 2, "ListByOwner": 3, "ListByStatus": 3, "AppendJournal": 4}
	for _, m := range methods {
		n, ok := arity[m]
		require.True(t, ok, "unknown store method %s", m)
		args := make([]any, n)
		for i := range args {
			args[i] = mock.Anything
		}
		ms.AssertNotCalled(t, m, args...)
	}
}

// ---- UpdateMyTicket (the owner)

func TestUpdateMyTicketAppendsTheAnswer(t *testing.T) {
	// an answer to ops hands the ticket back to them, in any other state the status stays
	next := map[TicketStatus]TicketStatus{
		TicketStatusWaitingForOwner: TicketStatusPendingResolve,
	}
	for from, to := range next {
		t.Run(string(from)+" to "+string(to), func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf(userOid, from), nil)
			ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, entry(to, userOid, "it is the red one")).
				Return(ticketOf(userOid, to), nil)
			c := setup(t, ms)
			c.reply = confirmed

			status, msg := c.call(toolMine, map[string]any{"id": ticketID, "message": "it is the red one"})

			require.Equal(t, http.StatusOK, status)
			assert.False(t, c.result(msg).IsError)
			require.Len(t, c.asked, 1)
			assert.Contains(t, c.asked[0], ticketID, "the draft names the ticket")
			assert.Contains(t, c.asked[0], "it is the red one", "the draft shows the answer")
			ms.AssertNumberOfCalls(t, "AppendJournal", 1) // the handler runs twice, the entry is added once
		})
	}
}

func TestUpdateMyTicketNeedsConfirmation(t *testing.T) {
	for name, reply := range map[string]map[string]any{"declined": declined, "cancelled": cancelled, "unchecked": unchecked} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf(userOid, TicketStatusPendingResolve), nil)
			c := setup(t, ms)
			c.reply = reply

			_, msg := c.call(toolMine, map[string]any{"id": ticketID, "message": "x"})

			res := c.result(msg)
			assert.True(t, res.IsError)
			assert.Contains(t, res.text(), noConfirm)
			never(t, ms, "AppendJournal")
		})
	}
}

func TestUpdateMyTicketRejectsWhatItCannotUpdate(t *testing.T) {
	cases := map[string]struct {
		args   map[string]any
		ticket Ticket
		getErr error
		want   string
	}{
		"someone else's": {args: map[string]any{"id": ticketID, "message": "x"}, ticket: ticketOf("other", TicketStatusPendingResolve), want: notFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, mock.Anything).Return(tc.ticket, tc.getErr).Maybe()
			c := setup(t, ms)
			c.reply = confirmed

			status, msg := c.call(toolMine, tc.args)

			require.Equal(t, http.StatusOK, status)
			res := c.result(msg)
			assert.True(t, res.IsError)
			assert.Contains(t, res.text(), tc.want)
			assert.Empty(t, c.asked, "the user is not asked about an update that cannot happen")
			never(t, ms, "AppendJournal")
		})
	}
}

func TestUpdateMyTicketHidesTicketsOfOthers(t *testing.T) {
	ms := &mockRepository{}
	ms.On("Get", mock.Anything, ticketID).Return(ticketOf("other", TicketStatusPendingResolve), nil).Once()
	ms.On("Get", mock.Anything, ticketID).Return(Ticket{}, ErrNotFound).Once()
	c := setup(t, ms)
	c.reply = confirmed
	args := map[string]any{"id": ticketID, "message": "x"}

	_, someoneElses := c.call(toolMine, args)
	_, missing := c.call(toolMine, args)

	assert.Equal(t, c.result(missing).text(), c.result(someoneElses).text(), "no hint whether the ticket exists")
}

func TestUpdateMyTicketWriteErrors(t *testing.T) {
	for name, err := range map[string]error{"deleted meanwhile": ErrNotFound, "changed meanwhile": ErrConflict, "store fails": assert.AnError} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf(userOid, TicketStatusPendingResolve), nil)
			ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, mock.Anything).Return(Ticket{}, err)
			c := setup(t, ms)
			c.reply = confirmed

			_, msg := c.call(toolMine, map[string]any{"id": ticketID, "message": "x"})

			assert.True(t, c.result(msg).IsError, "the model is told that it failed")
		})
	}
}

func TestUpdateMyTicketNeedsElicitationAndAToken(t *testing.T) {
	ms := &mockRepository{}
	c := setup(t, ms)
	args := map[string]any{"id": ticketID, "message": "x"}

	c.caps = map[string]any{}
	status, msg := c.call(toolMine, args)
	assert.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, msg.Error)
	assert.Equal(t, codeNoCap, msg.Error.Code)

	c.caps = map[string]any{"elicitation": map[string]any{}}
	for _, token := range []string{"", "garbage"} {
		c.token = token
		status, _ = c.call(toolMine, args)
		assert.Equal(t, http.StatusUnauthorized, status, "token %q", token)
	}
	never(t, ms, "Get", "AppendJournal")
}

// ---- UpdateTicket (ops)

func TestUpdateTicketSetsTheStatus(t *testing.T) {
	for _, to := range []TicketStatus{TicketStatusWaitingForOwner, TicketStatusResolved} {
		// ops work on anyone's approved ticket
		for _, from := range []TicketStatus{TicketStatusPendingResolve} {
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				ms := &mockRepository{}
				ms.On("Get", mock.Anything, ticketID).Return(ticketOf("some-owner", from), nil)
				ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, entry(to, opsOid, "please send a screenshot")).
					Return(ticketOf("some-owner", to), nil)
				c := setup(t, ms)
				c.as(opsOid, roleOps)
				c.reply = confirmed

				status, msg := c.call(toolUpdate, map[string]any{"id": ticketID, "message": "please send a screenshot", "status": string(to)})

				require.Equal(t, http.StatusOK, status)
				assert.False(t, c.result(msg).IsError)
				require.Len(t, c.asked, 1)
				assert.Contains(t, c.asked[0], ticketID)
				assert.Contains(t, c.asked[0], string(to), "the draft shows the new status")
				assert.Contains(t, c.asked[0], "please send a screenshot")
				ms.AssertNumberOfCalls(t, "AppendJournal", 1)
			})
		}
	}
}

func TestUpdateTicketNeedsConfirmation(t *testing.T) {
	for name, reply := range map[string]map[string]any{"declined": declined, "cancelled": cancelled, "unchecked": unchecked} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf("o", TicketStatusPendingResolve), nil)
			c := setup(t, ms)
			c.as(opsOid, roleOps)
			c.reply = reply

			_, msg := c.call(toolUpdate, map[string]any{"id": ticketID, "message": "x", "status": "waiting_for_owner"})

			res := c.result(msg)
			assert.True(t, res.IsError)
			assert.Contains(t, res.text(), noConfirm)
			never(t, ms, "AppendJournal")
		})
	}
}

func TestUpdateTicketRejectsWhatItCannotUpdate(t *testing.T) {
	cases := map[string]struct {
		args   map[string]any
		ticket Ticket
		getErr error
		want   string
	}{
		"empty message":       {args: map[string]any{"id": ticketID, "message": "", "status": "waiting_for_owner"}, want: "message"},
		"approval is not set": {args: map[string]any{"id": ticketID, "message": "x", "status": "pending_approval"}, want: oneOf},
		"not approved yet":    {args: map[string]any{"id": ticketID, "message": "x", "status": "resolved"}, ticket: ticketOf("o", TicketStatusPendingApproval), want: "not approved yet"},
		"open is not set":     {args: map[string]any{"id": ticketID, "message": "x", "status": "open"}, want: oneOf},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, mock.Anything).Return(tc.ticket, tc.getErr).Maybe()
			c := setup(t, ms)
			c.as(opsOid, roleOps)
			c.reply = confirmed

			status, msg := c.call(toolUpdate, tc.args)

			require.Equal(t, http.StatusOK, status)
			rejected, why := c.isRejected(msg) // a bad enum value may already fail on the schema
			assert.True(t, rejected, why)
			assert.Contains(t, why, tc.want)
			assert.Empty(t, c.asked)
			never(t, ms, "AppendJournal")
		})
	}
}

func TestUpdateTicketWriteErrors(t *testing.T) {
	for name, err := range map[string]error{"deleted meanwhile": ErrNotFound, "store fails": assert.AnError} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf("o", TicketStatusPendingResolve), nil)
			ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, mock.Anything).Return(Ticket{}, err)
			c := setup(t, ms)
			c.as(opsOid, roleOps)
			c.reply = confirmed

			_, msg := c.call(toolUpdate, map[string]any{"id": ticketID, "message": "x", "status": "resolved"})

			assert.True(t, c.result(msg).IsError)
		})
	}
}

func TestUpdateTicketIsForOpsOnly(t *testing.T) {
	args := map[string]any{"id": ticketID, "message": "x", "status": "resolved"}
	for name, setToken := range map[string]func(*client){
		"plain user":    func(c *client) { c.as(userOid) },
		"reviewer only": func(c *client) { c.as("rev", "esm:reviewer") },
	} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			setToken(c)

			status, _ := c.call(toolUpdate, args)

			assert.Equal(t, http.StatusForbidden, status)
			never(t, ms, "Get", "AppendJournal")
		})
	}
}

func TestUpdateTicketNeedsElicitationAndAToken(t *testing.T) {
	ms := &mockRepository{}
	c := setup(t, ms)
	c.as(opsOid, roleOps)
	args := map[string]any{"id": ticketID, "message": "x", "status": "resolved"}

	c.caps = map[string]any{}
	status, msg := c.call(toolUpdate, args)
	assert.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, msg.Error)
	assert.Equal(t, codeNoCap, msg.Error.Code)

	c.caps = map[string]any{"elicitation": map[string]any{}}
	for _, token := range []string{"", "garbage"} {
		c.token = token
		status, _ = c.call(toolUpdate, args)
		assert.Equal(t, http.StatusUnauthorized, status, "token %q", token)
	}
	never(t, ms, "Get", "AppendJournal")
}

func TestOpsAreAlsoUsersAndOwnersOfTheirTickets(t *testing.T) {
	ms := &mockRepository{}
	ms.On("Get", mock.Anything, ticketID).Return(ticketOf(opsOid, TicketStatusWaitingForOwner), nil)
	ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, entry(TicketStatusPendingResolve, opsOid, "mine")).Return(Ticket{}, nil)
	c := setup(t, ms)
	c.as(opsOid, roleOps)
	c.reply = confirmed

	_, msg := c.call(toolMine, map[string]any{"id": ticketID, "message": "mine"})

	assert.False(t, c.result(msg).IsError)
	ms.AssertExpectations(t)
}
