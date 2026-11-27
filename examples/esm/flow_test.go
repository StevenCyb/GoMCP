package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole life of a ticket against the real in-memory store, with a different user for each step.
type flow struct {
	t  *testing.T
	c  *client
	st *InMemoryRepository
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	st := &InMemoryRepository{tickets: map[string]Ticket{}}
	c := setup(t, st)
	c.reply = confirmed
	return &flow{t: t, c: c, st: st}
}

// do calls a tool as a user and expects the call itself to go through.
func (f *flow) do(oid string, roles []string, tool string, args map[string]any) toolResult {
	f.t.Helper()
	f.c.as(oid, roles...)
	status, msg := f.c.call(tool, args)
	require.Equal(f.t, http.StatusOK, status)
	return f.c.result(msg)
}

func (f *flow) create() string {
	f.t.Helper()
	res := f.do(userOid, nil, toolCreate, map[string]any{"description": "need a license", "type": "request"})
	require.False(f.t, res.IsError, res.text())
	return "T-0001"
}

func (f *flow) statusOf(id string) TicketStatus {
	f.t.Helper()
	got, err := f.st.Get(f.t.Context(), id)
	require.NoError(f.t, err)
	return got.CurrentStatus
}

func TestFlowApproveProcessAndResolve(t *testing.T) {
	f := newFlow(t)
	id := f.create()
	assert.Equal(t, TicketStatusPendingApproval, f.statusOf(id), "a new ticket waits for approval")

	// the reviewer sees it, the owner does not see it in the review queue
	pending := f.do(reviewerOid, []string{roleReview}, toolPending, nil)
	assert.Contains(t, pending.text(), id)

	res := f.do(reviewerOid, []string{roleReview}, toolReview, map[string]any{"id": id, "result": true, "reason": "ok"})
	require.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusPendingResolve, f.statusOf(id))
	assert.Equal(t, "[]", f.do(reviewerOid, []string{roleReview}, toolPending, nil).text(), "no longer waits for a review")

	// ops ask the owner, the owner answers, ops resolve
	res = f.do(opsOid, []string{roleOps}, toolUpdate, map[string]any{"id": id, "message": "which product?", "status": "waiting_for_owner"})
	require.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusWaitingForOwner, f.statusOf(id))

	res = f.do(userOid, nil, toolMine, map[string]any{"id": id, "message": "the pro one"})
	require.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusPendingResolve, f.statusOf(id), "the answer hands the ticket back to ops")

	res = f.do(opsOid, []string{roleOps}, toolUpdate, map[string]any{"id": id, "message": "license is in your mail", "status": "resolved"})
	require.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusResolved, f.statusOf(id))

	// the owner reads the whole conversation, in order
	got, err := f.st.Get(t.Context(), id)
	require.NoError(t, err)
	trail := make([][2]string, 0, len(got.Journals))
	for _, j := range got.Journals {
		trail = append(trail, [2]string{j.AuthorOid, j.Text})
	}
	assert.Equal(t, [][2]string{
		{reviewerOid, "ok"}, {opsOid, "which product?"}, {userOid, "the pro one"}, {opsOid, "license is in your mail"},
	}, trail)

	// final: nobody can change it anymore
	for _, res := range []toolResult{
		f.do(userOid, nil, toolMine, map[string]any{"id": id, "message": "thanks"}),
		f.do(opsOid, []string{roleOps}, toolUpdate, map[string]any{"id": id, "message": "x", "status": "resolved"}),
		f.do(reviewerOid, []string{roleReview}, toolReview, map[string]any{"id": id, "result": false, "reason": "x"}),
	} {
		assert.True(t, res.IsError)
	}
	assert.Equal(t, TicketStatusResolved, f.statusOf(id))
}

func TestFlowRejected(t *testing.T) {
	f := newFlow(t)
	id := f.create()

	res := f.do(reviewerOid, []string{roleReview}, toolReview, map[string]any{"id": id, "result": false, "reason": "not covered by the policy"})
	require.False(t, res.IsError, res.text())

	assert.Equal(t, TicketStatusRejected, f.statusOf(id))
	assert.True(t, f.do(opsOid, []string{roleOps}, toolUpdate, map[string]any{"id": id, "message": "x", "status": "resolved"}).IsError, "ops cannot process it")
	assert.True(t, f.do(userOid, nil, toolMine, map[string]any{"id": id, "message": "please"}).IsError, "rejected is final")
	own := f.do(userOid, nil, toolList, map[string]any{"status": "rejected"})
	assert.Contains(t, own.text(), id, "the owner sees the rejection and its reason in the journal")
	assert.Contains(t, own.text(), "not covered by the policy")
}

func TestFlowOpsCannotProcessAnUnapprovedTicket(t *testing.T) {
	f := newFlow(t)
	id := f.create()

	res := f.do(opsOid, []string{roleOps}, toolUpdate, map[string]any{"id": id, "message": "on it", "status": "resolved"})

	assert.True(t, res.IsError)
	assert.Contains(t, res.text(), "not approved yet")
	assert.Equal(t, TicketStatusPendingApproval, f.statusOf(id))
}

func TestFlowOwnerCanAddInformationWhileWaitingForApproval(t *testing.T) {
	f := newFlow(t)
	id := f.create()

	res := f.do(userOid, nil, toolMine, map[string]any{"id": id, "message": "it is urgent"})

	require.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusPendingApproval, f.statusOf(id), "the status does not change")
}

func TestFlowNobodyReviewsTheirOwnTicket(t *testing.T) {
	f := newFlow(t)
	// a reviewer creates a ticket
	f.do(reviewerOid, []string{roleReview}, toolCreate, map[string]any{"description": "my own request", "type": "request"})
	id := "T-0001"

	assert.Equal(t, "[]", f.do(reviewerOid, []string{roleReview}, toolPending, nil).text(), "not offered to the reviewer")
	res := f.do(reviewerOid, []string{roleReview}, toolReview, map[string]any{"id": id, "result": true, "reason": "ok"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.text(), "your own ticket")
	assert.Equal(t, TicketStatusPendingApproval, f.statusOf(id))

	// another reviewer can
	res = f.do("other-reviewer", []string{roleReview}, toolReview, map[string]any{"id": id, "result": true, "reason": "ok"})
	assert.False(t, res.IsError, res.text())
	assert.Equal(t, TicketStatusPendingResolve, f.statusOf(id))
}

func TestFlowUsersSeeOnlyTheirOwnTickets(t *testing.T) {
	f := newFlow(t)
	id := f.create()

	other := f.do("someone-else", nil, toolList, map[string]any{})
	assert.Equal(t, "[]", other.text())
	assert.True(t, f.do("someone-else", nil, toolMine, map[string]any{"id": id, "message": "x"}).IsError)
}
