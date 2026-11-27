package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	typeString  = "string"
	typeInteger = "integer"
	typeNumber  = "number"
	typeBoolean = "boolean"
	unset       = "(not set)"
)

// choice is one value of a field with a fixed set of values.
type choice struct {
	label string
	raw   json.RawMessage // nil: the field is left out
}

// field is one input of the form: a text box, or a list of choices for booleans and enums.
type field struct {
	name, typ, desc string
	required        bool
	in              textinput.Model
	choices         []choice // set for booleans and enums
	idx             int
}

func (f *field) isChoice() bool { return f.choices != nil }

// form is the argument form of a tool, built from its input schema.
type form struct {
	fields []*field
	focus  int
}

// schemaProp is the part of a property schema the form uses.
type schemaProp struct {
	Type        any               `json:"type"`
	Description string            `json:"description"`
	Enum        []json.RawMessage `json:"enum"`
	Default     json.RawMessage   `json:"default"`
}

func (p schemaProp) typeName() string {
	switch t := p.Type.(type) {
	case string:
		return t
	case []any: // ["string","null"]: the first one that is not null
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

// newForm builds the fields of a tool: required ones first, then the others by name.
func newForm(t caller.Tool) *form {
	names := make([]string, 0, len(t.InputSchema.Properties))
	for n := range t.InputSchema.Properties {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int {
		ra, rb := slices.Contains(t.InputSchema.Required, a), slices.Contains(t.InputSchema.Required, b)
		switch {
		case ra != rb && ra:
			return -1
		case ra != rb:
			return 1
		}
		return strings.Compare(a, b)
	})
	f := &form{}
	for _, n := range names {
		var p schemaProp
		_ = json.Unmarshal(t.InputSchema.Properties[n], &p)
		f.fields = append(f.fields, newField(n, p, slices.Contains(t.InputSchema.Required, n)))
	}
	f.setFocus()
	return f
}

func newField(name string, p schemaProp, required bool) *field {
	fl := &field{name: name, typ: p.typeName(), desc: p.Description, required: required, in: textinput.New()}
	fl.in.Prompt = ""
	fl.in.CharLimit = 0
	switch {
	case len(p.Enum) > 0:
		for _, raw := range p.Enum {
			fl.choices = append(fl.choices, choice{label: strings.Trim(string(raw), `"`), raw: raw})
		}
	case fl.typ == typeBoolean:
		fl.choices = []choice{{"true", json.RawMessage("true")}, {"false", json.RawMessage("false")}}
	}
	if fl.isChoice() {
		if !required {
			fl.choices = append([]choice{{label: unset}}, fl.choices...)
		}
		fl.idx = slices.IndexFunc(fl.choices, func(c choice) bool { return len(p.Default) > 0 && string(c.raw) == string(p.Default) })
		fl.idx = max(fl.idx, 0)
		return fl
	}
	switch fl.typ {
	case typeString:
		fl.in.Placeholder = "text"
		var s string
		if json.Unmarshal(p.Default, &s) == nil {
			fl.in.SetValue(s)
		}
	case typeInteger, typeNumber:
		fl.in.Placeholder = fl.typ
		if len(p.Default) > 0 {
			fl.in.SetValue(string(p.Default))
		}
	default:
		fl.in.Placeholder = "JSON"
		if len(p.Default) > 0 {
			fl.in.SetValue(string(p.Default))
		}
	}
	return fl
}

func (f *form) setFocus() {
	for i, fl := range f.fields {
		if i == f.focus {
			fl.in.Focus()
		} else {
			fl.in.Blur()
		}
	}
}

// move changes the focused field, wrapping around.
func (f *form) move(delta int) {
	if len(f.fields) == 0 {
		return
	}
	f.focus = (f.focus + delta + len(f.fields)) % len(f.fields)
	f.setFocus()
}

// atLast reports whether the focus is on the last field, where enter runs the tool.
func (f *form) atLast() bool { return len(f.fields) == 0 || f.focus == len(f.fields)-1 }

// update passes a key to the focused field.
func (f *form) update(msg tea.Msg) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	fl := f.fields[f.focus]
	if k, ok := msg.(tea.KeyMsg); ok && fl.isChoice() {
		switch k.String() {
		case "left", "h":
			fl.idx = (fl.idx - 1 + len(fl.choices)) % len(fl.choices)
		case "right", "l", " ":
			fl.idx = (fl.idx + 1) % len(fl.choices)
		}
		return nil
	}
	var cmd tea.Cmd
	fl.in, cmd = fl.in.Update(msg)
	return cmd
}

// args turns the inputs into the JSON object of the call, or says what is wrong.
func (f *form) args() (json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, fl := range f.fields {
		raw, err := fl.value()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fl.name, err)
		}
		if raw != nil {
			out[fl.name] = raw
		}
	}
	return json.Marshal(out)
}

func (fl *field) value() (json.RawMessage, error) {
	if fl.isChoice() {
		return fl.choices[fl.idx].raw, nil
	}
	text := strings.TrimSpace(fl.in.Value())
	if fl.typ == typeString {
		if text == "" && !fl.required {
			return nil, nil
		}
		return json.Marshal(fl.in.Value())
	}
	if text == "" {
		if fl.required {
			return nil, errors.New("is required")
		}
		return nil, nil
	}
	switch fl.typ {
	case typeInteger:
		if _, err := strconv.ParseInt(text, 10, 64); err != nil {
			return nil, errors.New("is not an integer")
		}
	case typeNumber:
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return nil, errors.New("is not a number")
		}
	default:
		if !json.Valid([]byte(text)) {
			return nil, errors.New("is not valid JSON")
		}
	}
	return json.RawMessage(text), nil
}

// view renders the fields.
func (f *form) view(width int) string {
	if len(f.fields) == 0 {
		return dim.Render("This tool takes no arguments. Press enter to run it.")
	}
	var b strings.Builder
	for i, fl := range f.fields {
		name := fl.name
		if fl.required {
			name += " *"
		}
		style := label
		marker := "  "
		if i == f.focus {
			style, marker = labelFocus, "▸ "
		}
		b.WriteString(marker + style.Render(name) + dim.Render("  "+fl.typ) + "\n")
		if fl.isChoice() {
			c := fl.choices[fl.idx].label
			if i == f.focus {
				c = "‹ " + c + " ›"
			}
			b.WriteString("    " + c + "\n")
		} else {
			fl.in.Width = max(width-6, 10)
			b.WriteString("    " + fl.in.View() + "\n")
		}
		if fl.desc != "" {
			b.WriteString("    " + dim.Render(firstLine(fl.desc)) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return first
}
