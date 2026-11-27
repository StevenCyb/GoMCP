package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func noFilter() any {
	return mock.MatchedBy(func(f TicketFilter) bool { return f.ID == nil && f.Status == nil })
}

func filter(id string, status TicketStatus) any {
	return mock.MatchedBy(func(f TicketFilter) bool {
		idOK := (id == "" && f.ID == nil) || (f.ID != nil && *f.ID == id)
		statusOK := (status == "" && f.Status == nil) || (f.Status != nil && *f.Status == status)
		return idOK && statusOK
	})
}

func decodeTickets(t *testing.T, c *client, msg rpcMessage) []Ticket {
	t.Helper()
	res := c.result(msg)
	require.False(t, res.IsError, res.text())
	var got []Ticket
	require.NoError(t, json.Unmarshal([]byte(res.text()), &got), res.text())
	return got
}

func TestListOwnTicketsWithoutFilter(t *testing.T) {
	want := []Ticket{
		{ID: "T-0001", OwnerOid: userOid, TicketType: TicketTypeRequest, CurrentStatus: TicketStatusPendingApproval, Text: "a", Journals: []Journal{}},
		{ID: "T-0002", OwnerOid: userOid, TicketType: TicketTypeIncident, CurrentStatus: TicketStatusResolved, Text: "b", Journals: []Journal{}},
	}
	ms := &mockRepository{}
	ms.On("ListByOwner", mock.Anything, userOid, noFilter()).Return(want, nil)
	c := setup(t, ms)

	status, msg := c.call(toolList, map[string]any{})

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, want, decodeTickets(t, c, msg))
	assert.Empty(t, c.asked, "listing asks nothing")
	ms.AssertNumberOfCalls(t, "ListByOwner", 1)
}

func TestListOwnTicketsFilters(t *testing.T) {
	cases := map[string]struct {
		args map[string]any
		want any
	}{
		"status":        {map[string]any{"status": "pending_approval"}, filter("", TicketStatusPendingApproval)},
		"id and status": {map[string]any{"id": "T-0001", "status": "waiting_for_owner"}, filter("T-0001", TicketStatusWaitingForOwner)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			ms.On("ListByOwner", mock.Anything, userOid, tc.want).Return([]Ticket{{ID: "T-0001"}}, nil)
			c := setup(t, ms)

			status, msg := c.call(toolList, tc.args)

			require.Equal(t, http.StatusOK, status)
			assert.Len(t, decodeTickets(t, c, msg), 1)
			ms.AssertExpectations(t)
		})
	}
}

func TestListOwnTicketsNoMatchIsAnEmptyList(t *testing.T) {
	ms := &mockRepository{}
	ms.On("ListByOwner", mock.Anything, userOid, mock.Anything).Return([]Ticket{}, nil)
	c := setup(t, ms)

	_, msg := c.call(toolList, map[string]any{"status": "rejected"})

	assert.Equal(t, "[]", c.result(msg).text(), "an empty array, not null")
}

func TestListOwnTicketsRejectsUnknownStatus(t *testing.T) {
	for _, status := range []string{"closed", "open"} {
		t.Run(status, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)

			code, msg := c.call(toolList, map[string]any{"status": status})

			require.Equal(t, http.StatusOK, code)
			rejected, why := c.isRejected(msg)
			assert.True(t, rejected, why)
			ms.AssertNotCalled(t, "ListByOwner", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestListOwnTicketsOwnerComesFromTheToken(t *testing.T) {
	ms := &mockRepository{}
	ms.On("ListByOwner", mock.Anything, userOid, mock.Anything).Return([]Ticket{}, nil)
	c := setup(t, ms)

	// a caller must not be able to name another owner, whatever the arguments say
	_, msg := c.call(toolList, map[string]any{"owner": "someone-else", "owner_oid": "someone-else", "status": "pending_approval"})

	if msg.Error == nil {
		assert.False(t, c.result(msg).IsError)
	}
	ms.AssertNotCalled(t, "ListByOwner", mock.Anything, "someone-else", mock.Anything)
}

func TestListOwnTicketsStoreError(t *testing.T) {
	ms := &mockRepository{}
	ms.On("ListByOwner", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
	c := setup(t, ms)

	status, msg := c.call(toolList, map[string]any{})

	require.Equal(t, http.StatusOK, status)
	assert.True(t, c.result(msg).IsError, "the model is told that it failed")
}

func TestListOwnTicketsNeedsAValidToken(t *testing.T) {
	for name, token := range map[string]string{"no token": "", "invalid token": "garbage"} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			c.token = token

			status, _ := c.call(toolList, map[string]any{})

			assert.Equal(t, http.StatusUnauthorized, status)
			ms.AssertNotCalled(t, "ListByOwner", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}
