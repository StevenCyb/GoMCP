// Package tui is the terminal UI of "gomcp client interactive": a list of the tools of a server, a form
// for the arguments of the chosen tool and the result of the call.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	title      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	label      = lipgloss.NewStyle()
	labelFocus = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dim        = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	bad        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	good       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

type screen int

const (
	screenList screen = iota
	screenForm
	screenResult
)

const logLimit = 4096

// logBuffer keeps the end of the server log, to explain a server that did not start.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	if l.buf.Len() > logLimit {
		l.buf = *bytes.NewBuffer(append([]byte(nil), l.buf.Bytes()[l.buf.Len()-logLimit:]...))
	}
	return len(p), nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.buf.String())
}

// Session is what the UI needs from a connection; *caller.Client is one.
type Session interface {
	Tools() ([]caller.Tool, error)
	Call(name string, args json.RawMessage) (json.RawMessage, error)
	Login() error
}

type toolItem struct{ caller.Tool }

func (t toolItem) Title() string       { return t.Name }
func (t toolItem) Description() string { return firstLine(t.Tool.Description) }
func (t toolItem) FilterValue() string { return t.Name + " " + t.Tool.Description }

type (
	toolsMsg  struct{ tools []caller.Tool }
	resultMsg struct {
		raw json.RawMessage
		err error
	}
	loginMsg struct{ err error }
	errMsg   struct{ err error }
)

type model struct {
	session       Session
	target        string
	oauth         bool
	log           *logBuffer
	screen        screen
	list          list.Model
	form          *form
	tool          caller.Tool
	vp            viewport.Model
	raw           json.RawMessage
	resultErr     error
	showJSON      bool
	busy, status  string
	statusIsError bool
	width, height int
}

func newModel(s Session, target string, oauth bool, log *logBuffer) *model {
	d := list.NewDefaultDelegate()
	l := list.New(nil, d, 0, 0)
	l.Title = "Tools of " + target
	l.Styles.Title = title
	l.SetShowHelp(true)
	l.DisableQuitKeybindings()
	l.SetStatusBarItemName("tool", "tools")
	l.AdditionalShortHelpKeys = func() []key.Binding {
		keys := []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
			key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload")),
		}
		if oauth {
			keys = append(keys, key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "log in")))
		}
		return append(keys, key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")))
	}
	return &model{session: s, target: target, oauth: oauth, log: log, list: l, busy: "Loading the tools…"}
}

func (m *model) Init() tea.Cmd { return m.loadTools() }

func (m *model) loadTools() tea.Cmd {
	s := m.session
	return func() tea.Msg {
		tools, err := s.Tools()
		if err != nil {
			return errMsg{err}
		}
		return toolsMsg{tools}
	}
}

func (m *model) call(args json.RawMessage) tea.Cmd {
	s, name := m.session, m.tool.Name
	return func() tea.Msg {
		raw, err := s.Call(name, args)
		return resultMsg{raw, err}
	}
}

func (m *model) login() tea.Cmd {
	s := m.session
	return func() tea.Msg { return loginMsg{s.Login()} }
}

func (m *model) fail(err error) {
	m.status, m.statusIsError = err.Error(), true
	var auth *caller.AuthError
	if errors.As(err, &auth) && m.oauth {
		m.status += " (press a to log in)"
	}
	if logs := m.log.String(); logs != "" && !strings.Contains(m.status, logs) {
		m.status += "\n" + logs
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, max(msg.Height-statusHeight(m.status), 5))
		m.vp.Width, m.vp.Height = msg.Width, max(msg.Height-4, 3)
		m.renderResult()
		return m, nil
	case toolsMsg:
		items := make([]list.Item, len(msg.tools))
		for i, t := range msg.tools {
			items[i] = toolItem{t}
		}
		m.busy, m.status = "", ""
		return m, m.list.SetItems(items)
	case errMsg:
		m.busy = ""
		m.fail(msg.err)
		m.list.SetSize(m.width, max(m.height-statusHeight(m.status), 5))
		return m, nil
	case loginMsg:
		m.busy = ""
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.busy, m.status = "Loading the tools…", "Logged in."
		return m, m.loadTools()
	case resultMsg:
		m.busy = ""
		m.raw, m.resultErr = msg.raw, msg.err
		m.screen = screenResult
		m.vp.GotoTop()
		m.renderResult()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy != "" {
			return m, nil // one request at a time
		}
		switch m.screen {
		case screenList:
			return m.updateList(msg)
		case screenForm:
			return m.updateForm(msg)
		case screenResult:
			return m.updateResult(msg)
		}
	}
	return m.passThrough(msg)
}

func (m *model) passThrough(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenList:
		m.list, cmd = m.list.Update(msg)
	case screenForm:
		cmd = m.form.update(msg)
	case screenResult:
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

func (m *model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.list.FilterState() == list.Filtering {
		return m.passThrough(msg)
	}
	switch msg.String() {
	case "enter":
		if it, ok := m.list.SelectedItem().(toolItem); ok {
			m.tool, m.form, m.screen, m.status = it.Tool, newForm(it.Tool), screenForm, ""
		}
		return m, nil
	case "r":
		m.busy, m.status = "Loading the tools…", ""
		return m, m.loadTools()
	case "a":
		if m.oauth {
			m.busy, m.status = "Waiting for the login in your browser…", ""
			return m, m.login()
		}
		return m, nil
	case "q":
		return m, tea.Quit
	case "esc":
		if m.list.FilterState() == list.Unfiltered {
			return m, tea.Quit
		}
	}
	return m.passThrough(msg)
}

func (m *model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen, m.status = screenList, ""
		return m, nil
	case "tab", "down":
		m.form.move(1)
		return m, nil
	case "shift+tab", "up":
		m.form.move(-1)
		return m, nil
	case "enter", "ctrl+r":
		if msg.String() == "enter" && !m.form.atLast() {
			m.form.move(1)
			return m, nil
		}
		args, err := m.form.args()
		if err != nil {
			m.status, m.statusIsError = err.Error(), true
			return m, nil
		}
		m.busy, m.status = "Calling "+m.tool.Name+"…", ""
		return m, m.call(args)
	}
	return m.passThrough(msg)
}

func (m *model) updateResult(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenForm
		return m, nil
	case "q":
		return m, tea.Quit
	case "j":
		m.showJSON = !m.showJSON
		m.renderResult()
		return m, nil
	case "a":
		if m.oauth {
			m.screen, m.busy = screenList, "Waiting for the login in your browser…"
			return m, m.login()
		}
	}
	return m.passThrough(msg)
}

func (m *model) renderResult() {
	var b strings.Builder
	var toolErr *caller.ToolError
	switch {
	case m.resultErr != nil && !errors.As(m.resultErr, &toolErr):
		b.WriteString(bad.Render(m.resultErr.Error()))
		var auth *caller.AuthError
		if errors.As(m.resultErr, &auth) && m.oauth {
			b.WriteString("\n\n" + dim.Render("Press a to log in."))
		}
	case m.showJSON && m.raw != nil:
		b.WriteString(caller.Indent(m.raw))
	case m.raw != nil:
		text := caller.Text(m.raw)
		if text == "" {
			text = caller.Indent(m.raw)
		}
		b.WriteString(text)
	}
	m.vp.SetContent(lipgloss.NewStyle().Width(max(m.width, 20)).Render(b.String()))
}

func statusHeight(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 2
}

func (m *model) View() string {
	var body, help string
	switch m.screen {
	case screenList:
		body = m.list.View()
	case screenForm:
		body = title.Render(m.tool.Name) + "\n"
		if d := strings.TrimSpace(m.tool.Description); d != "" {
			body += dim.Render(d) + "\n"
		}
		body += "\n" + m.form.view(m.width)
		help = "tab/↓ next · shift+tab/↑ previous · ←/→ choose · enter next, on the last field run · ctrl+r run · esc back"
	case screenResult:
		head := good.Render("✓ " + m.tool.Name)
		if m.resultErr != nil {
			head = bad.Render("✗ " + m.tool.Name)
			if _, ok := errors.AsType[*caller.ToolError](m.resultErr); ok {
				head += dim.Render("  tool error")
			}
		}
		body = head + "\n\n" + m.vp.View()
		help = "↑/↓ scroll · j " + map[bool]string{false: "raw JSON", true: "text"}[m.showJSON] + " · esc back · q quit"
	}
	if m.busy != "" {
		body += "\n" + dim.Render(m.busy)
	} else if m.status != "" {
		style := dim
		if m.statusIsError {
			style = bad
		}
		body += "\n" + style.Render(m.status)
	}
	if help != "" {
		body += "\n" + dim.Render(help)
	}
	return body
}

// Run connects as cfg says and shows the UI until the user quits.
func Run(ctx context.Context, cfg caller.Config) error {
	logs := &logBuffer{}
	cfg.Stderr = logs
	c, end, err := caller.Connect(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	_, runErr := tea.NewProgram(newModel(c, cfg.Target, cfg.OAuth, logs), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	_ = end()
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, tea.ErrProgramKilled) {
		return nil
	}
	return runErr //nolint:wrapcheck // the terminal error is shown as is
}
