package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/caller"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaJSON = `{
	"title":  {"type": "string", "description": "What to do.\nSecond line."},
	"count":  {"type": "integer"},
	"ratio":  {"type": "number"},
	"done":   {"type": "boolean"},
	"level":  {"type": "string", "enum": ["low", "high"], "default": "high"},
	"tags":   {"type": "array"}
}`

func tool(t *testing.T, required ...string) caller.Tool {
	t.Helper()
	var props map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(schemaJSON), &props))
	tl := caller.Tool{Name: "add", Description: "Adds a todo."}
	tl.InputSchema.Properties, tl.InputSchema.Required = props, required
	return tl
}

func typeInto(f *form, name, text string) {
	for i, fl := range f.fields {
		if fl.name == name {
			f.focus = i
			f.setFocus()
			fl.in.SetValue(text)
		}
	}
}

func TestFormOrdersRequiredFirst(t *testing.T) {
	f := newForm(tool(t, "title"))
	names := make([]string, len(f.fields))
	for i, fl := range f.fields {
		names[i] = fl.name
	}
	assert.Equal(t, []string{"title", "count", "done", "level", "ratio", "tags"}, names)
}

func TestFormBuildsArguments(t *testing.T) {
	f := newForm(tool(t, "title"))
	typeInto(f, "title", "milk")
	typeInto(f, "count", "3")
	typeInto(f, "ratio", "0.5")
	typeInto(f, "tags", `["a","b"]`)
	for _, fl := range f.fields {
		if fl.name == "done" {
			fl.idx = 1 // true: index 0 is "not set"
		}
	}
	args, err := f.args()
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"milk","count":3,"ratio":0.5,"tags":["a","b"],"done":true,"level":"high"}`, string(args),
		"the default of the enum is preselected, empty optional fields are left out")
}

func TestFormLeavesOutEmptyOptionalFields(t *testing.T) {
	f := newForm(tool(t, "title"))
	args, err := f.args()
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"","level":"high"}`, string(args), "a required string may be empty")
}

func TestFormRejectsBadValues(t *testing.T) {
	for name, tc := range map[string]struct{ field, text, want string }{
		"integer":  {"count", "1.5", "count: is not an integer"},
		"number":   {"ratio", "abc", "ratio: is not a number"},
		"json":     {"tags", "[", "tags: is not valid JSON"},
		"required": {"count", "", "count: is required"},
	} {
		t.Run(name, func(t *testing.T) {
			required := []string{"title"}
			if name == "required" {
				required = append(required, "count")
			}
			f := newForm(tool(t, required...))
			typeInto(f, "title", "x")
			typeInto(f, tc.field, tc.text)
			_, err := f.args()
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestFormChoicesCycle(t *testing.T) {
	f := newForm(tool(t))
	for i, fl := range f.fields {
		if fl.name == "level" {
			f.focus = i
		}
	}
	right := tea.KeyMsg{Type: tea.KeyRight}
	fl := f.fields[f.focus]
	f.update(right)
	assert.Equal(t, unset, fl.choices[fl.idx].label, "wraps from high to the unset entry")
	f.update(right)
	assert.Equal(t, "low", fl.choices[fl.idx].label)
}

type fakeSession struct {
	tools   []caller.Tool
	listErr error
	called  string
	args    json.RawMessage
	result  json.RawMessage
	err     error
	logins  int
}

func (s *fakeSession) Tools() ([]caller.Tool, error) { return s.tools, s.listErr }
func (s *fakeSession) Call(name string, args json.RawMessage) (json.RawMessage, error) {
	s.called, s.args = name, args
	return s.result, s.err
}
func (s *fakeSession) Login() error { s.logins++; return nil }

// step feeds a message to the model and runs the command it returns until no message is left.
func step(t *testing.T, m *model, msg tea.Msg) {
	t.Helper()
	_, cmd := m.Update(msg)
	for cmd != nil {
		out := cmd()
		if _, quit := out.(tea.QuitMsg); quit {
			return
		}
		if _, ok := out.(tea.BatchMsg); ok {
			return
		}
		_, cmd = m.Update(out)
	}
}

func keyOf(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newTestModel(s *fakeSession, oauth bool) *model {
	m := newModel(s, "demo", oauth, &logBuffer{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestFlowListFormResultAndBack(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t, "title")}, result: json.RawMessage(`{"content":[{"type":"text","text":"added milk"}]}`)}
	m := newTestModel(s, false)
	step(t, m, m.Init()())
	assert.Contains(t, m.View(), "add")
	assert.Contains(t, m.View(), "Adds a todo.")

	step(t, m, keyOf("enter"))
	require.Equal(t, screenForm, m.screen)
	assert.Contains(t, m.View(), "title *")

	typeInto(m.form, "title", "milk")
	step(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Equal(t, screenResult, m.screen)
	assert.Equal(t, "add", s.called)
	assert.JSONEq(t, `{"title":"milk","level":"high"}`, string(s.args))
	assert.Contains(t, m.View(), "added milk")

	step(t, m, keyOf("j"))
	assert.Contains(t, m.View(), `"content"`, "j shows the raw result")

	step(t, m, keyOf("esc"))
	assert.Equal(t, screenForm, m.screen, "esc leaves the result for the form, which keeps its values")
	assert.Equal(t, "milk", m.form.fields[0].in.Value())
	step(t, m, keyOf("esc"))
	assert.Equal(t, screenList, m.screen)
}

func TestFormEnterWalksThenRuns(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t)}, result: json.RawMessage(`{"content":[]}`)}
	m := newTestModel(s, false)
	step(t, m, m.Init()())
	step(t, m, keyOf("enter"))
	for range len(m.form.fields) - 1 {
		step(t, m, keyOf("enter"))
		assert.Equal(t, screenForm, m.screen, "enter moves on")
	}
	step(t, m, keyOf("enter"))
	assert.Equal(t, screenResult, m.screen, "enter on the last field runs the tool")
}

func TestInvalidFormStaysOnForm(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t, "count")}}
	m := newTestModel(s, false)
	step(t, m, m.Init()())
	step(t, m, keyOf("enter"))
	step(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	assert.Equal(t, screenForm, m.screen)
	assert.Empty(t, s.called)
	assert.Contains(t, m.View(), "count: is required")
}

func TestToolErrorAndProtocolErrorAreShown(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t)}, result: json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"it broke"}]}`),
		err: &caller.ToolError{Message: "it broke"}}
	m := newTestModel(s, false)
	step(t, m, m.Init()())
	step(t, m, keyOf("enter"))
	step(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	assert.Contains(t, m.View(), "tool error")
	assert.Contains(t, m.View(), "it broke")

	s.result, s.err = nil, errors.New("server closed")
	step(t, m, keyOf("esc"))
	step(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	assert.Contains(t, m.View(), "server closed")
}

func TestReloadAndLogin(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t)}}
	m := newTestModel(s, true)
	step(t, m, m.Init()())
	assert.Len(t, m.list.Items(), 1)

	s.tools = append(s.tools, caller.Tool{Name: "second"})
	step(t, m, keyOf("r"))
	assert.Len(t, m.list.Items(), 2, "r asks the server for the tools again")

	step(t, m, keyOf("a"))
	assert.Equal(t, 1, s.logins)
	assert.Len(t, m.list.Items(), 2, "after the login the tools are loaded again")
}

func TestLoginKeyIsIgnoredWithoutOAuth(t *testing.T) {
	s := &fakeSession{tools: []caller.Tool{tool(t)}}
	m := newTestModel(s, false)
	step(t, m, m.Init()())
	step(t, m, keyOf("a"))
	assert.Zero(t, s.logins)
}

func TestAuthErrorOnLoadSuggestsLogin(t *testing.T) {
	s := &fakeSession{listErr: &caller.AuthError{Status: 401, Hint: "pass --token"}}
	m := newTestModel(s, true)
	step(t, m, m.Init()())
	assert.Contains(t, m.View(), "press a to log in")
}

func TestQuit(t *testing.T) {
	m := newTestModel(&fakeSession{tools: []caller.Tool{tool(t)}}, false)
	step(t, m, m.Init()())
	for _, k := range []string{"q", "esc"} {
		_, cmd := m.Update(keyOf(k))
		require.NotNil(t, cmd, k)
		_, quit := cmd().(tea.QuitMsg)
		assert.True(t, quit, k)
	}
}

func TestLogBufferKeepsTheEnd(t *testing.T) {
	var l logBuffer
	_, _ = l.Write([]byte(strings.Repeat("x", logLimit)))
	_, _ = l.Write([]byte("END"))
	assert.Len(t, l.String(), logLimit)
	assert.True(t, strings.HasSuffix(l.String(), "END"))
}
