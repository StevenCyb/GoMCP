package mcp_test

import (
	"errors"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tasksExt  = "io.modelcontextprotocol/tasks"
	keyTaskID = "taskId"
)

func withTasks(caps map[string]any) map[string]any {
	out := map[string]any{"extensions": map[string]any{tasksExt: map[string]any{}}}
	maps.Copy(out, caps)
	return out
}

type taskObject struct {
	ResultType    string         `json:"resultType"`
	TaskID        string         `json:"taskId"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"statusMessage"`
	CreatedAt     string         `json:"createdAt"`
	LastUpdatedAt string         `json:"lastUpdatedAt"`
	TTLMs         *int64         `json:"ttlMs"`
	PollMs        int64          `json:"pollIntervalMs"`
	InputRequests map[string]any `json:"inputRequests"`
	Result        map[string]any `json:"result"`
	Error         *rpc.Error     `json:"error"`
}

type taskEnv struct {
	srv     *mcp.Server
	c       *testClient
	release chan struct{}
	started chan struct{}
	stopped chan struct{}
}

func newTaskEnv(t *testing.T, caps map[string]any, opts ...mcp.Option) *taskEnv {
	t.Helper()
	env := &taskEnv{release: make(chan struct{}), started: make(chan struct{}, 4), stopped: make(chan struct{}, 4)}
	s := newBasicServer(opts...)
	env.srv = s
	add := func(def mcp.ToolDef, h func(mcp.Context, mcp.Empty) (string, error)) {
		require.NoError(t, mcp.AddTool(s, def, h))
	}
	add(mcp.ToolDef{Name: "quick", Task: mcp.TaskOptional, TaskTTL: time.Minute, TaskPoll: 2 * time.Second},
		func(mcp.Context, mcp.Empty) (string, error) { return "done", nil })
	add(mcp.ToolDef{Name: "waits", Task: mcp.TaskOptional}, func(mcp.Context, mcp.Empty) (string, error) {
		env.started <- struct{}{}
		<-env.release
		return "released", nil
	})
	add(mcp.ToolDef{Name: "cancellable", Task: mcp.TaskOptional}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		env.started <- struct{}{}
		<-ctx.Done()
		env.stopped <- struct{}{}
		return "", ctx.Err()
	})
	add(mcp.ToolDef{Name: "needs_task", Task: mcp.TaskRequired}, func(mcp.Context, mcp.Empty) (string, error) { return "ok", nil })
	add(mcp.ToolDef{Name: "plain"}, func(mcp.Context, mcp.Empty) (string, error) { return "plain", nil })
	add(mcp.ToolDef{Name: "tool_error", Task: mcp.TaskOptional}, func(mcp.Context, mcp.Empty) (string, error) { return "", mcp.ToolError("nope") })
	add(mcp.ToolDef{Name: "protocol_error", Task: mcp.TaskOptional},
		func(mcp.Context, mcp.Empty) (string, error) { return "", mcp.ErrInvalidParams("bad") })
	add(mcp.ToolDef{Name: "panics", Task: mcp.TaskOptional}, func(mcp.Context, mcp.Empty) (string, error) { panic("boom") })
	add(mcp.ToolDef{Name: "short_lived", Task: mcp.TaskOptional, TaskTTL: 60 * time.Millisecond},
		func(mcp.Context, mcp.Empty) (string, error) { return "done", nil })
	add(mcp.ToolDef{Name: "roots", Task: mcp.TaskOptional, Requires: []string{mcp.CapRoots}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		roots, err := ctx.Roots()
		if err != nil {
			return "", err
		}
		return roots[0].URI, nil
	})
	env.c = connect(t, newServer(t, s), caps)
	return env
}

func (e *taskEnv) create(t *testing.T, tool string) taskObject {
	t.Helper()
	res := e.c.once("tools/call", map[string]any{keyName: tool}, nil)
	task := result[taskObject](t, res)
	require.Equal(t, "task", task.ResultType, "expected a task handle")
	return task
}

func (e *taskEnv) get(t *testing.T, id string) taskObject {
	t.Helper()
	return result[taskObject](t, e.c.call("tasks/get", map[string]any{keyTaskID: id}))
}

func (e *taskEnv) waitStatus(t *testing.T, id, status string) taskObject {
	t.Helper()
	var got taskObject
	require.Eventually(t, func() bool {
		got = e.get(t, id)
		return got.Status == status
	}, 3*time.Second, 10*time.Millisecond, "task never reached %s (last %s)", status, got.Status)
	return got
}

func TestWithoutTheExtensionToolsRunDirectly(t *testing.T) {
	env := newTaskEnv(t, nil)
	res := result[toolResult](t, callTool(t, env.c, "quick", nil))
	assert.Equal(t, "done", res.Content[0][keyText], "an optional task tool runs directly for clients without the extension")

	required := env.c.post("tools/call", map[string]any{keyName: "needs_task"}, nil)
	_ = required.Body.Close()
	assert.Equal(t, http.StatusBadRequest, required.StatusCode)
	out := env.c.once("tools/call", map[string]any{keyName: "needs_task"}, nil)
	require.NotNil(t, out.Error)
	assert.Equal(t, rpc.CodeMissingCapability, out.Error.Code)
	assert.JSONEq(t, `{"requiredCapabilities":{"extensions":{"io.modelcontextprotocol/tasks":{}}}}`, string(rpc.MustJSON(out.Error.Data)))

	for _, method := range []string{"tasks/get", "tasks/update", "tasks/cancel"} {
		res := env.c.once(method, map[string]any{keyTaskID: "x"}, nil)
		require.NotNil(t, res.Error, method)
		assert.Equal(t, rpc.CodeMissingCapability, res.Error.Code, method)
	}
}

func TestServerDecidesToCreateATask(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	task := env.create(t, "quick")

	assert.NotEmpty(t, task.TaskID)
	assert.Contains(t, []string{"working", "completed"}, task.Status)
	assert.NotEmpty(t, task.CreatedAt)
	assert.NotEmpty(t, task.LastUpdatedAt)
	require.NotNil(t, task.TTLMs)
	assert.Equal(t, int64(60000), *task.TTLMs)
	assert.Equal(t, int64(2000), task.PollMs)

	plain := result[toolResult](t, callTool(t, env.c, "plain", nil))
	assert.Equal(t, "plain", plain.Content[0][keyText], "tools without task support answer directly")

	// a task parameter of the client is ignored
	res := env.c.once("tools/call", map[string]any{keyName: "plain", "task": map[string]any{"ttl": 1}}, nil)
	assert.Equal(t, "plain", result[toolResult](t, res).Content[0][keyText])
}

func TestPollTaskToCompletion(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	created := env.create(t, "quick")

	done := env.waitStatus(t, created.TaskID, "completed")
	assert.Equal(t, "complete", done.ResultType, "tasks/get itself is a complete result")
	require.NotNil(t, done.Result)
	content, _ := done.Result["content"].([]any)
	require.Len(t, content, 1)
	assert.Equal(t, "done", content[0].(map[string]any)[keyText])
}

func TestFailedAndCompletedWithError(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))

	toolErr := env.create(t, "tool_error")
	done := env.waitStatus(t, toolErr.TaskID, "completed")
	assert.Equal(t, true, done.Result["isError"], "a tool error is a completed task with an error result")

	proto := env.create(t, "protocol_error")
	failed := env.waitStatus(t, proto.TaskID, "failed")
	require.NotNil(t, failed.Error, "only a protocol error fails a task")
	assert.Equal(t, rpc.CodeInvalidParams, failed.Error.Code)
	assert.Nil(t, failed.Result)

	panicked := env.create(t, "panics")
	assert.Equal(t, "failed", env.waitStatus(t, panicked.TaskID, "failed").Status, "a panic does not take the server down")
}

func TestCancelTask(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	created := env.create(t, "cancellable")
	<-env.started

	ack := result[map[string]any](t, env.c.call("tasks/cancel", map[string]any{keyTaskID: created.TaskID}))
	assert.Equal(t, "complete", ack["resultType"])
	select {
	case <-env.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler context was not cancelled")
	}
	assert.Equal(t, "cancelled", env.get(t, created.TaskID).Status)
	_ = result[map[string]any](t, env.c.call("tasks/cancel", map[string]any{keyTaskID: created.TaskID}))
	assert.Equal(t, "cancelled", env.get(t, created.TaskID).Status, "cancelling again changes nothing")
}

func TestUnknownTask(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	for _, method := range []string{"tasks/get", "tasks/update", "tasks/cancel"} {
		res := env.c.call(method, map[string]any{keyTaskID: "nope"})
		require.NotNil(t, res.Error, method)
		assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code, method)

		missing := env.c.call(method, map[string]any{})
		require.NotNil(t, missing.Error, method)
	}
}

func TestTaskExpires(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	created := env.create(t, "short_lived")
	env.waitStatus(t, created.TaskID, "completed")
	time.Sleep(150 * time.Millisecond)
	res := env.c.call("tasks/get", map[string]any{keyTaskID: created.TaskID})
	require.NotNil(t, res.Error, "the task is gone after its ttl")
	assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code)
}

func TestTaskAsksForInputAndGetsItThroughUpdate(t *testing.T) {
	env := newTaskEnv(t, withTasks(map[string]any{mcp.CapRoots: map[string]any{}}))
	created := env.create(t, "roots")

	waiting := env.waitStatus(t, created.TaskID, "input_required")
	require.Len(t, waiting.InputRequests, 1)
	request, _ := waiting.InputRequests["roots-1"].(map[string]any)
	assert.Equal(t, "roots/list", request["method"])

	ack := result[map[string]any](t, env.c.call("tasks/update", map[string]any{
		keyTaskID: created.TaskID,
		"inputResponses": map[string]any{
			"unknown-1": map[string]any{"x": 1}, // ignored
			"roots-1":   map[string]any{mcp.CapRoots: []any{map[string]any{keyURI: "file:///work"}}},
		},
	}))
	assert.Equal(t, "complete", ack["resultType"])

	done := env.waitStatus(t, created.TaskID, "completed")
	content, _ := done.Result["content"].([]any)
	assert.Equal(t, "file:///work", content[0].(map[string]any)[keyText])
}

func TestTaskInputWithoutCapabilityFails(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	res := env.c.once("tools/call", map[string]any{keyName: "roots"}, nil)
	require.NotNil(t, res.Error, "the tool needs roots, the client has none: the task is not even created")
	assert.Equal(t, rpc.CodeMissingCapability, res.Error.Code)
}

func TestTasksBelongToTheirCaller(t *testing.T) {
	s := authedServer(t, oauthScheme())
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "quick", Task: mcp.TaskOptional},
		func(mcp.Context, mcp.Empty) (string, error) { return "done", nil }))
	srv := newServer(t, s)
	alice, bob := connectAs(t, srv, "reader", withTasks(nil)), connectAs(t, srv, "writer", withTasks(nil))

	created := result[taskObject](t, alice.once("tools/call", map[string]any{keyName: "quick"}, nil))
	require.Equal(t, "task", created.ResultType)
	_ = result[taskObject](t, alice.call("tasks/get", map[string]any{keyTaskID: created.TaskID}))

	res := bob.call("tasks/get", map[string]any{keyTaskID: created.TaskID})
	require.NotNil(t, res.Error, "another caller cannot see the task")
	assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code)
}

func TestTaskStatusReachesListeners(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil))
	created := env.create(t, "waits")
	<-env.started

	l := env.c.listen(map[string]any{"taskIds": []string{created.TaskID, "unknown"}})
	ack := l.next()
	assert.JSONEq(t, `{"taskIds":["`+created.TaskID+`"]}`, string(rpc.MustJSON(mustField(t, ack.Params, "notifications"))), "unknown ids are not acknowledged")

	close(env.release)
	note := l.next()
	assert.Equal(t, "notifications/tasks", note.Method)
	var detail taskObject
	require.NoError(t, jsonUnmarshal(note.Params, &detail))
	assert.Equal(t, created.TaskID, detail.TaskID)
	assert.Equal(t, "completed", detail.Status)
	assert.NotNil(t, detail.Result, "the notification carries the full task")
}

func TestToolErrorsOfTaskToolsStayErrors(t *testing.T) {
	env := newTaskEnv(t, nil)
	res := result[toolResult](t, callTool(t, env.c, "tool_error", nil))
	assert.True(t, res.IsError)
	require.Error(t, errors.New("sanity"))
}

func TestTasksAreLimited(t *testing.T) {
	s := newBasicServer(mcp.WithAuth(limitScheme()), mcp.WithLimits(mcp.Limits{Tasks: 2}))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "waits", Task: mcp.TaskOptional},
		func(mcp.Context, mcp.Empty) (string, error) { <-release; return "", nil }))
	srv := newServer(t, s)
	for _, who := range []string{"global-a", "global-b"} {
		task := result[taskObject](t, connectAs(t, srv, who, withTasks(nil)).once("tools/call", map[string]any{keyName: "waits"}, nil))
		require.Equal(t, "task", task.ResultType)
	}
	res := connectAs(t, srv, "global-c", withTasks(nil)).once("tools/call", map[string]any{keyName: "waits"}, nil)
	require.NotNil(t, res.Error, "the task over the limit is refused")
	assert.Equal(t, rpc.CodeInvalidRequest, res.Error.Code)
	assert.Contains(t, res.Error.Message, "try again later")
}

func TestTasksAreLimitedPerCaller(t *testing.T) {
	s := newBasicServer(mcp.WithAuth(limitScheme()), mcp.WithLimits(mcp.Limits{TasksPerCaller: 1}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "fast", Task: mcp.TaskOptional},
		func(mcp.Context, mcp.Empty) (string, error) { return "done", nil }))
	srv := newServer(t, s)
	flooder, other := connectAs(t, srv, "task-flooder", withTasks(nil)), connectAs(t, srv, "task-other", withTasks(nil))

	require.Equal(t, "task", result[taskObject](t, flooder.once("tools/call", map[string]any{keyName: "fast"}, nil)).ResultType)
	res := flooder.once("tools/call", map[string]any{keyName: "fast"}, nil)
	require.NotNil(t, res.Error, "finished tasks count until they expire")
	assert.Equal(t, rpc.CodeInvalidRequest, res.Error.Code)
	assert.Equal(t, "task", result[taskObject](t, other.once("tools/call", map[string]any{keyName: "fast"}, nil)).ResultType, "other callers are not affected")
}

func TestTaskWithoutTTLGetsTheDefaultAndExpires(t *testing.T) {
	env := newTaskEnv(t, withTasks(nil), mcp.WithLimits(mcp.Limits{TaskTTL: 60 * time.Millisecond}))
	created := env.create(t, "tool_error") // the tool sets no ttl
	require.NotNil(t, created.TTLMs)
	assert.EqualValues(t, 60, *created.TTLMs)
	env.waitStatus(t, created.TaskID, "completed")
	time.Sleep(150 * time.Millisecond)
	res := env.c.call("tasks/get", map[string]any{keyTaskID: created.TaskID})
	require.NotNil(t, res.Error, "the task is gone after the default ttl")
}

func TestTaskStopsWaitingForInput(t *testing.T) {
	env := newTaskEnv(t, withTasks(map[string]any{mcp.CapRoots: map[string]any{}}), mcp.WithLimits(mcp.Limits{TaskInputTimeout: 50 * time.Millisecond}))
	created := env.create(t, "roots")
	done := env.waitStatus(t, created.TaskID, "completed")
	assert.Equal(t, true, done.Result["isError"], "an unanswered question ends the task")
}
