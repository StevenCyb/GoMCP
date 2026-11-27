package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTicketMatcher(typ TicketType, text string) any {
	return mock.MatchedBy(func(tk Ticket) bool {
		return tk.ID == "" && tk.OwnerOid == userOid && tk.TicketType == typ && tk.Text == text &&
			tk.CurrentStatus == TicketStatusPendingApproval && tk.Journals != nil && len(tk.Journals) == 0 &&
			time.Since(tk.Time) < time.Minute
	})
}

func TestCreateTicketConfirmed(t *testing.T) {
	ms := &mockRepository{}
	ms.On("Create", mock.Anything, newTicketMatcher(TicketTypeIncident, "VPN is down")).
		Return(Ticket{ID: "T-0001", OwnerOid: userOid, TicketType: TicketTypeIncident, CurrentStatus: TicketStatusPendingApproval, Text: "VPN is down", Journals: []Journal{}}, nil)
	c := setup(t, ms)
	c.reply = confirmed

	status, msg := c.call(toolCreate, map[string]any{"description": "VPN is down", "type": "incident"})

	require.Equal(t, http.StatusOK, status)
	res := c.result(msg)
	require.False(t, res.IsError, res.text())
	var got Ticket
	require.NoError(t, json.Unmarshal(res.StructuredContent, &got))
	assert.Equal(t, "T-0001", got.ID)
	assert.Equal(t, TicketTypeIncident, got.TicketType)
	assert.Equal(t, TicketStatusPendingApproval, got.CurrentStatus)
	assert.Equal(t, userOid, got.OwnerOid, "the owner is the oid of the token")
	assert.Empty(t, got.Journals)

	require.Len(t, c.asked, 1, "one confirmation")
	assert.Contains(t, c.asked[0], "incident", "the draft shows the type")
	assert.Contains(t, c.asked[0], "VPN is down", "the draft shows the description")
	ms.AssertNumberOfCalls(t, "Create", 1) // the handler runs twice, the ticket is created once
}

func TestCreateTicketNotCreatedWithoutConfirmation(t *testing.T) {
	for name, reply := range map[string]map[string]any{"declined": declined, "cancelled": cancelled, "unchecked": unchecked} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			c.reply = reply

			status, msg := c.call(toolCreate, map[string]any{"description": "x", "type": "request"})

			require.Equal(t, http.StatusOK, status)
			res := c.result(msg)
			assert.True(t, res.IsError)
			assert.Contains(t, res.text(), "no ticket was created")
			assert.Len(t, c.asked, 1)
			ms.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}
}

func TestCreateTicketRejectsBadInputBeforeAsking(t *testing.T) {
	cases := map[string]map[string]any{
		"empty description": {"description": "", "type": "request"},
		"unknown type":      {"description": "x", "type": "complaint"},
		"no arguments":      {},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			c.reply = confirmed

			status, msg := c.call(toolCreate, args)

			require.Equal(t, http.StatusOK, status)
			rejected, why := c.isRejected(msg)
			assert.True(t, rejected, why)
			assert.Empty(t, c.asked, "the user is not bothered with an invalid draft")
			ms.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}
}

func TestCreateTicketStoreError(t *testing.T) {
	ms := &mockRepository{}
	ms.On("Create", mock.Anything, mock.Anything).Return(Ticket{}, assert.AnError)
	c := setup(t, ms)
	c.reply = confirmed

	status, msg := c.call(toolCreate, map[string]any{"description": "x", "type": "request"})

	require.Equal(t, http.StatusOK, status)
	assert.True(t, c.result(msg).IsError, "the model is told that it failed")
	ms.AssertNumberOfCalls(t, "Create", 1)
}

func TestCreateTicketNeedsElicitation(t *testing.T) {
	ms := &mockRepository{}
	c := setup(t, ms)
	c.caps = map[string]any{}

	status, msg := c.call(toolCreate, map[string]any{"description": "x", "type": "request"})

	assert.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, msg.Error)
	assert.Equal(t, codeNoCap, msg.Error.Code)
	ms.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCreateTicketNeedsAValidToken(t *testing.T) {
	for name, token := range map[string]string{"no token": "", "invalid token": "garbage"} {
		t.Run(name, func(t *testing.T) {
			ms := &mockRepository{}
			c := setup(t, ms)
			c.token = token

			status, _ := c.call(toolCreate, map[string]any{"description": "x", "type": "request"})

			assert.Equal(t, http.StatusUnauthorized, status)
			ms.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}
}
