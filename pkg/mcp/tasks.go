package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

// Task states of the tasks extension.
const (
	taskWorking       = "working"
	taskInputRequired = "input_required"
	taskCompleted     = "completed"
	taskFailed        = "failed"
	taskCancelled     = "cancelled"
)

func terminal(status string) bool {
	return status == taskCompleted || status == taskFailed || status == taskCancelled
}

// taskEntry is a tool call running in the background.
type taskEntry struct {
	id      string
	owner   string
	created time.Time
	ttl     time.Duration
	poll    time.Duration
	cancel  context.CancelFunc
	srv     *Server

	mu      sync.Mutex
	status  string
	message string
	updated time.Time
	expires time.Time
	result  any
	rpcErr  *rpc.Error
	pending map[string]inputRequest
	waiters map[string]chan json.RawMessage
}

// snapshotLocked is the task object of the protocol.
func (t *taskEntry) snapshotLocked() map[string]any {
	m := map[string]any{
		"taskId": t.id, "status": t.status,
		"createdAt": t.created.UTC().Format(time.RFC3339Nano), "lastUpdatedAt": t.updated.UTC().Format(time.RFC3339Nano),
		"ttlMs": nil,
	}
	if t.message != "" {
		m["statusMessage"] = t.message
	}
	if t.ttl > 0 {
		m["ttlMs"] = t.ttl.Milliseconds()
	}
	if t.poll > 0 {
		m["pollIntervalMs"] = t.poll.Milliseconds()
	}
	return m
}

// detailed adds what the state of the task carries: the outstanding input requests, the result or the error.
func (t *taskEntry) detailed() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.snapshotLocked()
	switch t.status {
	case taskInputRequired:
		requests := make(map[string]any, len(t.pending))
		for key, q := range t.pending {
			entry := map[string]any{"method": q.method}
			if q.params != nil {
				entry["params"] = q.params
			}
			requests[key] = entry
		}
		m["inputRequests"] = requests
	case taskCompleted:
		m["result"] = t.result
	case taskFailed:
		m["error"] = t.rpcErr
	}
	return m
}

// setStatus moves the task to a new state and tells the listeners; terminal states are final.
func (t *taskEntry) setStatus(status, message string) bool {
	t.mu.Lock()
	if terminal(t.status) || t.status == status {
		t.mu.Unlock()
		return false
	}
	t.status, t.message, t.updated = status, message, time.Now()
	if terminal(status) {
		t.expires = t.updated.Add(t.ttl)
	}
	t.mu.Unlock()

	detail := t.detailed()
	t.srv.subs.publish("notifications/tasks", detail, func(f subscriptionFilter) bool { return slices.Contains(f.TaskIDs, t.id) })
	return true
}

// finish records the outcome of the tool call. Only a protocol error makes a task fail; a tool
// result with isError is a completed task.
func (t *taskEntry) finish(res any, rpcErr *rpc.Error) {
	t.mu.Lock()
	if terminal(t.status) {
		t.mu.Unlock()
		return
	}
	t.result, t.rpcErr = res, rpcErr
	t.mu.Unlock()
	if rpcErr != nil {
		t.setStatus(taskFailed, rpcErr.Message)
		return
	}
	t.setStatus(taskCompleted, "")
}

// ask puts a question to the client and waits for tasks/update to answer it.
func (t *taskEntry) ask(c *callContext, key, method string, params any) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	t.mu.Lock()
	if t.pending == nil {
		t.pending, t.waiters = map[string]inputRequest{}, map[string]chan json.RawMessage{}
	}
	t.pending[key] = inputRequest{key: key, method: method, params: params}
	t.waiters[key] = ch
	t.mu.Unlock()
	t.setStatus(taskInputRequired, "waiting for input")

	timeout := time.NewTimer(t.srv.limits.TaskInputTimeout)
	defer timeout.Stop()
	var raw json.RawMessage
	var err error
	select {
	case raw = <-ch:
	case <-c.Done():
		err = fmt.Errorf("waiting for the client: %w", c.Err())
	case <-timeout.C:
		err = fmt.Errorf("the client did not answer within %s", t.srv.limits.TaskInputTimeout)
	}

	t.mu.Lock()
	delete(t.pending, key)
	delete(t.waiters, key)
	stillWaiting := len(t.pending) > 0
	t.mu.Unlock()
	if err == nil && !stillWaiting {
		t.mu.Lock()
		if t.status == taskInputRequired {
			t.status, t.message, t.updated = taskWorking, "", time.Now()
		}
		t.mu.Unlock()
	}
	return raw, err
}

// answer delivers the responses of tasks/update; unknown or answered keys are ignored.
func (t *taskEntry) answer(responses map[string]json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, raw := range responses {
		if ch, ok := t.waiters[key]; ok {
			select {
			case ch <- raw:
			default:
			}
		}
	}
}

func (t *taskEntry) expired(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.expires.IsZero() && now.After(t.expires)
}

// taskStore holds the tasks of the server.
type taskStore struct {
	mu       sync.Mutex
	tasks    map[string]*taskEntry
	perOwner map[string]int
	swept    time.Time
}

func newTaskStore() *taskStore {
	return &taskStore{tasks: map[string]*taskEntry{}, perOwner: map[string]int{}}
}

// add stores the task unless the server or its owner holds too many.
func (s *taskStore) add(t *taskEntry, lim Limits) *rpc.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(false)
	if len(s.tasks) >= lim.Tasks || s.perOwner[t.owner] >= lim.TasksPerCaller {
		s.sweepLocked(true) // finished tasks may just have run out
	}
	if len(s.tasks) >= lim.Tasks {
		return rpc.InvalidRequest("too many tasks on the server, try again later")
	}
	if s.perOwner[t.owner] >= lim.TasksPerCaller {
		return rpc.InvalidRequest("too many tasks of this caller, try again later")
	}
	s.tasks[t.id] = t
	s.perOwner[t.owner]++
	return nil
}

// get returns the task unless its ttl ran out.
func (s *taskStore) get(id string) *taskEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(false)
	t := s.tasks[id]
	if t != nil && t.expired(time.Now()) {
		s.removeLocked(t)
		return nil
	}
	return t
}

func (s *taskStore) removeLocked(t *taskEntry) {
	delete(s.tasks, t.id)
	if s.perOwner[t.owner]--; s.perOwner[t.owner] <= 0 {
		delete(s.perOwner, t.owner)
	}
}

// sweepLocked drops the tasks whose ttl ran out. It scans the whole store, so unless forced it
// runs at most once a second.
func (s *taskStore) sweepLocked(force bool) {
	now := time.Now()
	if !force && now.Sub(s.swept) < time.Second {
		return
	}
	s.swept = now
	for _, t := range s.tasks {
		if t.expired(now) {
			s.removeLocked(t)
		}
	}
}

// tasksEnabledLocked reports whether any tool can run as a task; the caller holds s.mu.
func (s *Server) tasksEnabledLocked() bool {
	for _, t := range s.tools {
		if t.def.Task == TaskOptional || t.def.Task == TaskRequired {
			return true
		}
	}
	return false
}

// startTask runs a tool call in the background and returns the task handle right away.
func (s *Server) startTask(ctx *callContext, t *tool, params json.RawMessage) (any, *rpc.Error) {
	taskCtx, cancel := context.WithCancel(s.base)
	now := time.Now()
	ttl := t.def.TaskTTL
	if ttl <= 0 {
		ttl = s.limits.TaskTTL
	}
	entry := &taskEntry{
		id: rand.Text(), owner: ctx.principal.Subject, created: now, updated: now, status: taskWorking,
		ttl: ttl, poll: t.def.TaskPoll, cancel: cancel, srv: s,
	}
	if rpcErr := s.tasks.add(entry, s.limits); rpcErr != nil {
		cancel()
		return nil, rpcErr
	}

	tc := &callContext{
		Context: taskCtx, srv: s, meta: ctx.meta, caps: ctx.caps, principal: ctx.principal,
		responses: map[string]json.RawMessage{}, state: map[string]string{}, counters: map[string]int{}, stateOut: map[string]string{},
		task: entry,
	}
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				entry.finish(nil, rpc.Internal(fmt.Sprint("task panicked: ", r)))
			}
		}()
		out, err := s.chainFor(s.toolCore(t), t.def.Use)(tc, Request{Kind: KindTool, Name: t.def.Name, Params: params})
		res, rpcErr := toolResult(out, err)
		entry.finish(res, rpcErr)
	}()

	entry.mu.Lock()
	handle := entry.snapshotLocked()
	entry.mu.Unlock()
	handle["resultType"] = "task"
	return handle, nil
}

// taskFor finds the task of a tasks/* request: the client must support the extension and own the task.
func (s *Server) taskFor(ctx *callContext, params json.RawMessage) (*taskEntry, *rpc.Error) {
	if !ctx.caps.hasExtension(tasksExtension) {
		return nil, rpc.MissingCapability(extensionObject(tasksExtension))
	}
	var p struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TaskID == "" {
		return nil, rpc.InvalidParams("a taskId is required")
	}
	entry := s.tasks.get(p.TaskID)
	if entry == nil || entry.owner != ctx.principal.Subject {
		return nil, rpc.InvalidParams("unknown task " + p.TaskID)
	}
	return entry, nil
}

func (s *Server) getTask(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	entry, rpcErr := s.taskFor(ctx, params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return entry.detailed(), nil
}

func (s *Server) updateTask(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	entry, rpcErr := s.taskFor(ctx, params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var p struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, rpc.InvalidParams("invalid tasks/update params: " + err.Error())
	}
	entry.answer(p.InputResponses)
	return map[string]any{}, nil
}

func (s *Server) cancelTask(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	entry, rpcErr := s.taskFor(ctx, params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if entry.setStatus(taskCancelled, "cancelled by the client") {
		entry.cancel()
	}
	return map[string]any{}, nil
}
