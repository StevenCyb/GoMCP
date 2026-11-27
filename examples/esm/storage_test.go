package api

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryCreateAssignsUniqueIDs(t *testing.T) {
	m := &InMemoryRepository{tickets: map[string]Ticket{}}

	first, err := m.Create(context.Background(), Ticket{Text: "a"})
	require.NoError(t, err)
	second, err := m.Create(context.Background(), Ticket{Text: "b"})
	require.NoError(t, err)

	assert.Equal(t, "T-0001", first.ID)
	assert.Equal(t, "T-0002", second.ID)
	assert.Equal(t, "a", m.tickets["T-0001"].Text)
}

func TestInMemoryCreateIsSafeForConcurrentUse(t *testing.T) {
	m := &InMemoryRepository{tickets: map[string]Ticket{}}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_, err := m.Create(context.Background(), Ticket{})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Len(t, m.tickets, 50, "no id was handed out twice")
}

func TestInMemoryListByOwner(t *testing.T) {
	ctx := context.Background()
	m := &InMemoryRepository{tickets: map[string]Ticket{}}
	mine := []Ticket{
		{OwnerOid: "me", CurrentStatus: TicketStatusPendingApproval},
		{OwnerOid: "me", CurrentStatus: TicketStatusResolved},
		{OwnerOid: "me", CurrentStatus: TicketStatusPendingApproval},
	}
	for _, tk := range mine {
		_, err := m.Create(ctx, tk)
		require.NoError(t, err)
	}
	theirs, err := m.Create(ctx, Ticket{OwnerOid: "other", CurrentStatus: TicketStatusPendingApproval})
	require.NoError(t, err)
	ids := func(list []Ticket) []string {
		out := make([]string, 0, len(list))
		for _, tk := range list {
			out = append(out, tk.ID)
		}
		return out
	}
	open, resolved, rejected := TicketStatusPendingApproval, TicketStatusResolved, TicketStatusRejected
	id1, missing := "T-0001", "T-9999"

	cases := map[string]struct {
		owner string
		f     TicketFilter
		want  []string
	}{
		"all of the owner, ordered by id": {"me", TicketFilter{}, []string{"T-0001", "T-0002", "T-0003"}},
		"by status":                       {"me", TicketFilter{Status: &open}, []string{"T-0001", "T-0003"}},
		"by other status":                 {"me", TicketFilter{Status: &resolved}, []string{"T-0002"}},
		"by id":                           {"me", TicketFilter{ID: &id1}, []string{"T-0001"}},
		"by id and status":                {"me", TicketFilter{ID: &id1, Status: &open}, []string{"T-0001"}},
		"id and status do not match":      {"me", TicketFilter{ID: &id1, Status: &resolved}, []string{}},
		"unknown id":                      {"me", TicketFilter{ID: &missing}, []string{}},
		"no ticket in status":             {"me", TicketFilter{Status: &rejected}, []string{}},
		"someone else's id is not found":  {"me", TicketFilter{ID: &theirs.ID}, []string{}},
		"the other owner sees only own":   {"other", TicketFilter{}, []string{theirs.ID}},
		"unknown owner":                   {"nobody", TicketFilter{}, []string{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := m.ListByOwner(ctx, tc.owner, tc.f)
			require.NoError(t, err)
			assert.NotNil(t, got, "an empty list, not nil")
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestInMemoryGet(t *testing.T) {
	ctx := context.Background()
	m := &InMemoryRepository{tickets: map[string]Ticket{}}
	created, err := m.Create(ctx, Ticket{Text: "a"})
	require.NoError(t, err)

	got, err := m.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Text)

	_, err = m.Get(ctx, "T-9999")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestInMemoryAppendJournal(t *testing.T) {
	ctx := context.Background()
	m := &InMemoryRepository{tickets: map[string]Ticket{}}
	created, err := m.Create(ctx, Ticket{CurrentStatus: TicketStatusPendingApproval, Journals: []Journal{}})
	require.NoError(t, err)

	first, err := m.AppendJournal(ctx, created.ID, TicketStatusPendingApproval, Journal{Status: TicketStatusWaitingForOwner, Text: "question"})
	require.NoError(t, err)
	second, err := m.AppendJournal(ctx, created.ID, TicketStatusWaitingForOwner, Journal{Status: TicketStatusPendingResolve, Text: "answer"})
	require.NoError(t, err)

	assert.Equal(t, TicketStatusWaitingForOwner, first.CurrentStatus)
	assert.Len(t, first.Journals, 1, "an earlier result is not changed by a later append")
	assert.Equal(t, TicketStatusPendingResolve, second.CurrentStatus, "the status of the last entry is the current one")
	assert.Equal(t, []string{"question", "answer"}, []string{second.Journals[0].Text, second.Journals[1].Text})
	stored, err := m.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, second, stored)

	_, err = m.AppendJournal(ctx, created.ID, TicketStatusPendingApproval, Journal{Status: TicketStatusResolved})
	assert.ErrorIs(t, err, ErrConflict, "the ticket is no longer in the expected status")
	unchanged, err := m.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, second, unchanged, "a refused append changes nothing")

	_, err = m.AppendJournal(ctx, "T-9999", TicketStatusPendingApproval, Journal{})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestInMemoryListByStatus(t *testing.T) {
	ctx := context.Background()
	m := &InMemoryRepository{tickets: map[string]Ticket{}}
	for _, tk := range []Ticket{
		{OwnerOid: "a", CurrentStatus: TicketStatusPendingApproval},
		{OwnerOid: "b", CurrentStatus: TicketStatusPendingApproval},
		{OwnerOid: "a", CurrentStatus: TicketStatusPendingResolve},
		{OwnerOid: "c", CurrentStatus: TicketStatusPendingApproval},
	} {
		_, err := m.Create(ctx, tk)
		require.NoError(t, err)
	}
	ids := func(list []Ticket) []string {
		out := make([]string, 0, len(list))
		for _, tk := range list {
			out = append(out, tk.ID)
		}
		return out
	}

	all, err := m.ListByStatus(ctx, TicketStatusPendingApproval, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"T-0001", "T-0002", "T-0004"}, ids(all), "ordered by id")

	withoutA, err := m.ListByStatus(ctx, TicketStatusPendingApproval, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"T-0002", "T-0004"}, ids(withoutA), "the excluded owner's tickets are left out")

	none, err := m.ListByStatus(ctx, TicketStatusResolved, "")
	require.NoError(t, err)
	assert.NotNil(t, none, "an empty list, not nil")
	assert.Empty(t, none)
}

func TestTicketStatusFromAndClosed(t *testing.T) {
	for _, s := range []TicketStatus{TicketStatusPendingApproval, TicketStatusRejected, TicketStatusPendingResolve, TicketStatusWaitingForOwner, TicketStatusResolved} {
		assert.Equal(t, s, TicketStatusFrom(string(s)), "round trip of %s", s)
	}
	for _, old := range []string{"open", "pending", "", "RESOLVED", "closed", "invalid"} {
		assert.Equal(t, TicketStatusInvalid, TicketStatusFrom(old), old)
	}
	closed := map[TicketStatus]bool{TicketStatusRejected: true, TicketStatusResolved: true}
	for _, s := range []TicketStatus{TicketStatusPendingApproval, TicketStatusRejected, TicketStatusPendingResolve, TicketStatusWaitingForOwner, TicketStatusResolved} {
		assert.Equal(t, closed[s], s.Closed(), s)
	}
}
