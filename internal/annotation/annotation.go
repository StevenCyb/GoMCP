// Package annotation parses @MCP.Name("primary", key="value", flag) comment lines.
package annotation

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const prefix = "@MCP."

// Kind is the type of an argument value.
type Kind int

// Value kinds.
const (
	KindString Kind = iota
	KindNumber
	KindIdent
)

// Value is the value of an argument.
type Value struct {
	Kind Kind
	Str  string // string content or identifier
	Num  float64
}

// Arg is one argument; Key is empty for positional arguments.
type Arg struct {
	Key   string
	Value Value
}

// Annotation is one parsed @MCP.* line.
type Annotation struct {
	Name string
	Args []Arg
}

// Error is a syntax error with its position.
type Error struct {
	Column int // 1-based
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("column %d: %s", e.Column, e.Msg)
}

// Positional returns the first positional argument.
func (a Annotation) Positional() (string, bool) {
	all := a.Positionals()
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}

// Positionals returns all positional string and identifier arguments.
func (a Annotation) Positionals() []string {
	var out []string
	for _, arg := range a.Args {
		if arg.Key == "" && arg.Value.Kind != KindNumber {
			out = append(out, arg.Value.Str)
		}
	}
	return out
}

// PositionalNumber returns the first positional number argument.
func (a Annotation) PositionalNumber() (float64, bool) {
	for _, arg := range a.Args {
		if arg.Key == "" && arg.Value.Kind == KindNumber {
			return arg.Value.Num, true
		}
	}
	return 0, false
}

// String returns a keyed string (or identifier) argument.
func (a Annotation) String(key string) (string, bool) {
	for _, arg := range a.Args {
		if arg.Key == key && arg.Value.Kind != KindNumber {
			return arg.Value.Str, true
		}
	}
	return "", false
}

// Number returns a keyed number argument.
func (a Annotation) Number(key string) (float64, bool) {
	for _, arg := range a.Args {
		if arg.Key == key && arg.Value.Kind == KindNumber {
			return arg.Value.Num, true
		}
	}
	return 0, false
}

// Flag reports whether a bare identifier argument with that name is present.
func (a Annotation) Flag(name string) bool {
	for _, arg := range a.Args {
		if arg.Key == "" && arg.Value.Kind == KindIdent && arg.Value.Str == name {
			return true
		}
	}
	return false
}

// ParseLine parses one comment line. The bool is false if the line is not an annotation.
func ParseLine(line string) (Annotation, bool, error) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return Annotation{}, false, nil
	}
	p := &parser{src: trimmed, pos: len(prefix), base: len(line) - len(trimmed)}
	a, err := p.annotation()
	return a, true, err
}

type parser struct {
	src  string
	pos  int
	base int // columns before src
}

func (p *parser) fail(msg string, args ...any) error {
	return &Error{Column: p.base + p.pos + 1, Msg: fmt.Sprintf(msg, args...)}
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() rune {
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return r
}

func (p *parser) skipSpace() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) annotation() (Annotation, error) {
	start := p.pos
	for !p.eof() && isAlnum(p.peek()) {
		p.pos++
	}
	if p.pos == start {
		return Annotation{}, p.fail("annotation name expected")
	}
	a := Annotation{Name: p.src[start:p.pos]}

	p.skipSpace()
	if !p.eof() && p.src[p.pos] == '(' {
		p.pos++
		args, err := p.arguments()
		if err != nil {
			return Annotation{}, err
		}
		a.Args = args
		p.skipSpace()
	}
	for !p.eof() && p.src[p.pos] == '.' { // a linter may have added a period to the comment
		p.pos++
		p.skipSpace()
	}
	if !p.eof() {
		return Annotation{}, p.fail("unexpected text after annotation")
	}
	return a, nil
}

func (p *parser) arguments() ([]Arg, error) {
	var args []Arg
	seen := map[string]bool{}
	p.skipSpace()
	if !p.eof() && p.src[p.pos] == ')' {
		p.pos++
		return nil, nil
	}
	for {
		arg, err := p.argument()
		if err != nil {
			return nil, err
		}
		if arg.Key != "" {
			if seen[arg.Key] {
				return nil, p.fail("duplicate argument %q", arg.Key)
			}
			seen[arg.Key] = true
		}
		args = append(args, arg)

		p.skipSpace()
		switch {
		case p.eof():
			return nil, p.fail("expected ')'")
		case p.src[p.pos] == ')':
			p.pos++
			return args, nil
		case p.src[p.pos] == ',':
			p.pos++
		default:
			return nil, p.fail("expected ',' or ')'")
		}
	}
}

func (p *parser) argument() (Arg, error) {
	p.skipSpace()
	v, err := p.value()
	if err != nil {
		return Arg{}, err
	}
	p.skipSpace()
	if v.Kind == KindIdent && !p.eof() && p.src[p.pos] == '=' && !strings.Contains(v.Str, ".") {
		p.pos++
		p.skipSpace()
		value, err := p.value()
		if err != nil {
			return Arg{}, err
		}
		return Arg{Key: v.Str, Value: value}, nil
	}
	return Arg{Value: v}, nil
}

func (p *parser) value() (Value, error) {
	if p.eof() {
		return Value{}, p.fail("value expected")
	}
	r := p.peek()
	switch {
	case r == '"':
		return p.stringValue()
	case r == '-' || unicode.IsDigit(r):
		return p.numberValue()
	case r == '_' || unicode.IsLetter(r):
		start := p.pos
		for !p.eof() && (isAlnum(p.peek()) || p.peek() == '_' || p.peek() == '.') {
			p.pos++
		}
		return Value{Kind: KindIdent, Str: p.src[start:p.pos]}, nil
	default:
		return Value{}, p.fail("value expected")
	}
}

func (p *parser) numberValue() (Value, error) {
	start := p.pos
	for !p.eof() && (p.src[p.pos] == '-' || p.src[p.pos] == '.' || (p.src[p.pos] >= '0' && p.src[p.pos] <= '9')) {
		p.pos++
	}
	n, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return Value{}, p.fail("invalid number %q", p.src[start:p.pos])
	}
	return Value{Kind: KindNumber, Num: n}, nil
}

func (p *parser) stringValue() (Value, error) {
	p.pos++ // opening quote
	var sb strings.Builder
	for !p.eof() {
		r := p.peek()
		switch r {
		case '"':
			p.pos++
			return Value{Kind: KindString, Str: sb.String()}, nil
		case '\\':
			p.pos++
			if p.eof() {
				return Value{}, p.fail("unterminated string")
			}
			switch esc := p.peek(); esc {
			case '"', '\\':
				sb.WriteRune(esc)
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			default:
				return Value{}, p.fail("unknown escape \\%c", esc)
			}
			p.pos += utf8.RuneLen(p.peek())
		default:
			sb.WriteRune(r)
			p.pos += utf8.RuneLen(r)
		}
	}
	return Value{}, p.fail("unterminated string")
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
