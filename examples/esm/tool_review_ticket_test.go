package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func asReviewer(c *client) { c.as(reviewerOid, roleReview) }

// ---- ListPendingReviews

func TestListPendingReviews(t *testing.T) {
	want := []Ticket{ticketOf("a", TicketStatusPendingApproval), {ID: "T-0002", OwnerOid: "b", CurrentStatus: TicketStatusPendingApproval, Journals: []Journal{}}}
	ms := &mockRepository{}
	// the reviewer's own tickets are left out
	ms.On("ListByStatus", mock.Anything, TicketStatusPendingApproval, reviewerOid).Return(want, nil)
	c := setup(t, ms)
	asReviewer(c)

	status, msg := c.call(toolPending, map[string]any{})

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, want, decodeTickets(t, c, msg))
	ms.AssertExpectations(t)
}

func TestListPendingReviewsNoneIsAnEmptyList(t *testing.T) {
	ms := &mockRepository{}
	ms.On("ListByStatus", mock.Anything, mock.Anything, mock.Anything).Return([]Ticket{}, nil)
	c := setup(t, ms)
	asReviewer(c)

	_, msg := c.call(toolPending, map[string]any{})

	assert.Equal(t, "[]", c.result(msg).text())
}

func TestListPendingReviewsStoreError(t *testing.T) {
	ms := &mockRepository{}
	ms.On("ListByStatus", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
	c := setup(t, ms)
	asReviewer(c)

	_, msg := c.call(toolPending, map[string]any{})

	assert.True(t, c.result(msg).IsError)
}

func TestListPendingReviewsIsForReviewersOnly(t *testing.T) {
	for name, setToken := range map[string]func(*client){
		"plain user": func(c *client) { c.as(userOid) },
		"ops only":   func(c *client) { c.as(opsOid, roleOps) },
		"no token":   func(c *client) { c.token = "" },
		"bad token":  func(c *client) { c.token = "garbage" },
	} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			setToken(c)

			status, _ := c.call(toolPending, map[string]any{})

			assert.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, status)
			assert.NotEqual(t, http.StatusOK, status)
			ms.AssertNotCalled(t, "ListByStatus", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// ---- ReviewTicket

func TestReviewTicketDecides(t *testing.T) {
	cases := map[string]struct {
		result bool
		to     TicketStatus
		verb   string
	}{
		"approve": {true, TicketStatusPendingResolve, "Approve"},
		"reject":  {false, TicketStatusRejected, "Reject"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf("owner", TicketStatusPendingApproval), nil)
			ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, entry(tc.to, reviewerOid, "fits the policy")).Return(ticketOf("owner", tc.to), nil)
			c := setup(t, ms)
			asReviewer(c)
			c.reply = confirmed

			status, msg := c.call(toolReview, map[string]any{"id": ticketID, "result": tc.result, "reason": "fits the policy"})

			require.Equal(t, http.StatusOK, status)
			assert.False(t, c.result(msg).IsError)
			require.Len(t, c.asked, 1)
			assert.Contains(t, c.asked[0], tc.verb)
			assert.Contains(t, c.asked[0], ticketID)
			assert.Contains(t, c.asked[0], "fits the policy", "the draft shows the reason")
			ms.AssertNumberOfCalls(t, "AppendJournal", 1)
		})
	}
}

func TestReviewTicketNeedsConfirmation(t *testing.T) {
	for name, reply := range map[string]map[string]any{"declined": declined, "cancelled": cancelled, "unchecked": unchecked} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf("owner", TicketStatusPendingApproval), nil)
			c := setup(t, ms)
			asReviewer(c)
			c.reply = reply

			_, msg := c.call(toolReview, map[string]any{"id": ticketID, "result": true, "reason": "ok"})

			res := c.result(msg)
			assert.True(t, res.IsError)
			assert.Contains(t, res.text(), noConfirm)
			ms.AssertNotCalled(t, "AppendJournal", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestReviewTicketRejectsWhatItCannotReview(t *testing.T) {
	args := func(extra map[string]any) map[string]any {
		a := map[string]any{"id": ticketID, "result": true, "reason": "ok"}
		for k, v := range extra {
			if v == nil {
				delete(a, k)
			} else {
				a[k] = v
			}
		}
		return a
	}
	cases := map[string]struct {
		args   map[string]any
		ticket Ticket
		getErr error
		want   string
	}{
		"empty reason":   {args: args(map[string]any{"reason": ""}), want: "reason"},
		"unknown ticket": {args: args(nil), getErr: ErrNotFound, want: notFound},
		"not waiting":    {args: args(nil), ticket: ticketOf("o", TicketStatusPendingResolve), want: "does not wait for a review"},
		"own ticket":     {args: args(nil), ticket: ticketOf(reviewerOid, TicketStatusPendingApproval), want: "your own ticket"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, mock.Anything).Return(tc.ticket, tc.getErr).Maybe()
			c := setup(t, ms)
			asReviewer(c)
			c.reply = confirmed

			status, msg := c.call(toolReview, tc.args)

			require.Equal(t, http.StatusOK, status)
			rejected, why := c.isRejected(msg)
			assert.True(t, rejected, why)
			assert.Contains(t, why, tc.want)
			assert.Empty(t, c.asked, "the reviewer is not asked about a decision that cannot happen")
			ms.AssertNotCalled(t, "AppendJournal", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestReviewTicketWriteErrors(t *testing.T) {
	for name, err := range map[string]error{"deleted meanwhile": ErrNotFound, "store fails": assert.AnError} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("Get", mock.Anything, ticketID).Return(ticketOf("o", TicketStatusPendingApproval), nil)
			ms.On("AppendJournal", mock.Anything, ticketID, mock.Anything, mock.Anything).Return(Ticket{}, err)
			c := setup(t, ms)
			asReviewer(c)
			c.reply = confirmed

			_, msg := c.call(toolReview, map[string]any{"id": ticketID, "result": true, "reason": "ok"})

			assert.True(t, c.result(msg).IsError)
		})
	}
}

func TestReviewTicketIsForReviewersOnly(t *testing.T) {
	args := map[string]any{"id": ticketID, "result": true, "reason": "ok"}
	for name, setToken := range map[string]func(*client){
		"plain user": func(c *client) { c.as(userOid) },
		"ops only":   func(c *client) { c.as(opsOid, roleOps) },
	} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			setToken(c)

			status, _ := c.call(toolReview, args)

			assert.Equal(t, http.StatusForbidden, status)
			ms.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
		})
	}

	c := setup(t, &mockRepository{})
	for _, token := range []string{"", "garbage"} {
		c.token = token
		status, _ := c.call(toolReview, args)
		assert.Equal(t, http.StatusUnauthorized, status, "token %q", token)
	}
}

func TestReviewTicketNeedsElicitation(t *testing.T) {
	c := setup(t, &mockRepository{})
	asReviewer(c)
	c.caps = map[string]any{}

	status, msg := c.call(toolReview, map[string]any{"id": ticketID, "result": true, "reason": "ok"})

	assert.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, msg.Error)
	assert.Equal(t, codeNoCap, msg.Error.Code)
}
