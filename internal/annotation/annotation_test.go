package annotation_test

import (
	"testing"

	"github.com/StevenCyb/GoMCP/internal/annotation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, line string) annotation.Annotation {
	t.Helper()
	a, ok, err := annotation.ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok, "not recognized as an annotation: %q", line)
	return a
}

func TestParseNameOnly(t *testing.T) {
	for _, line := range []string{"@MCP.Subscribable", "  @MCP.Subscribable", "@MCP.Subscribable()", "@MCP.Subscribable   ", "@MCP.Subscribable ( )"} {
		a := parse(t, line)
		assert.Equal(t, "Subscribable", a.Name)
		assert.Empty(t, a.Args)
	}
}

func TestParseTrailingPeriod(t *testing.T) {
	// linters such as godot add a period to the last line of a comment
	for _, line := range []string{"@MCP.Setup.", "@MCP.Setup().", "@MCP.Setup. ", "@MCP.Setup(). "} {
		a := parse(t, line)
		assert.Equal(t, "Setup", a.Name, line)
		assert.Empty(t, a.Args, line)
	}
	a := parse(t, `@MCP.Tool("add_note", title="Add note").`)
	name, ok := a.Positional()
	require.True(t, ok)
	assert.Equal(t, "add_note", name)
	title, _ := a.String("title")
	assert.Equal(t, "Add note", title)
}

func TestParseNotAnAnnotation(t *testing.T) {
	for _, line := range []string{"", "just text", "// @MCP.Tool(\"x\")", "see @MCP.Tool in the docs", "@Other.Tool(\"x\")", "@mcp.Tool(\"x\")"} {
		_, ok, err := annotation.ParseLine(line)
		require.NoError(t, err)
		assert.False(t, ok, line)
	}
}

func TestParseArguments(t *testing.T) {
	a := parse(t, `@MCP.Tool("add_note", title="Add note", priority=0.8, count=-2, readOnly, mode=optional, ttl="10m")`)
	assert.Equal(t, "Tool", a.Name)

	name, ok := a.Positional()
	require.True(t, ok)
	assert.Equal(t, "add_note", name)

	title, ok := a.String("title")
	require.True(t, ok)
	assert.Equal(t, "Add note", title)

	prio, ok := a.Number("priority")
	require.True(t, ok)
	assert.InDelta(t, 0.8, prio, 1e-9)

	count, ok := a.Number("count")
	require.True(t, ok)
	assert.InDelta(t, -2, count, 1e-9)

	assert.True(t, a.Flag("readOnly"))
	assert.False(t, a.Flag("destructive"))

	mode, ok := a.String("mode")
	require.True(t, ok, "identifiers are valid values")
	assert.Equal(t, "optional", mode)

	ttl, ok := a.String("ttl")
	require.True(t, ok)
	assert.Equal(t, "10m", ttl)

	_, ok = a.String("missing")
	assert.False(t, ok)
	_, ok = a.Number("title")
	assert.False(t, ok, "a string is not a number")
}

func TestPositionalNumber(t *testing.T) {
	a := parse(t, `@MCP.PageSize(50)`)
	n, ok := a.PositionalNumber()
	require.True(t, ok)
	assert.InDelta(t, 50, n, 1e-9)
	_, hasString := a.Positional()
	assert.False(t, hasString)

	_, ok = parse(t, `@MCP.PageSize("x")`).PositionalNumber()
	assert.False(t, ok)
}

func TestParseStringEscapesAndSpecialCharacters(t *testing.T) {
	a := parse(t, `@MCP.Instructions("Use \"notes\", then say hi (politely).\nBye \\ done")`)
	text, ok := a.Positional()
	require.True(t, ok)
	assert.Equal(t, "Use \"notes\", then say hi (politely).\nBye \\ done", text)
}

func TestParseMultiplePositionals(t *testing.T) {
	a := parse(t, `@MCP.AuthScopes("notes:read", "notes:write")`)
	assert.Equal(t, []string{"notes:read", "notes:write"}, a.Positionals())
	first, ok := a.Positional()
	require.True(t, ok)
	assert.Equal(t, "notes:read", first)
}

func TestParseIdentifiersWithDots(t *testing.T) {
	a := parse(t, `@MCP.Use(api.LoggingMiddleware)`)
	assert.Equal(t, []string{"api.LoggingMiddleware"}, a.Positionals())
	assert.True(t, a.Flag("api.LoggingMiddleware"))
}

func TestParseUnicode(t *testing.T) {
	a := parse(t, `@MCP.Tool("grüße", description="Sagt Hallo – ✓")`)
	name, _ := a.Positional()
	assert.Equal(t, "grüße", name)
	desc, _ := a.String("description")
	assert.Equal(t, "Sagt Hallo – ✓", desc)
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, line, contains string }{
		{"unterminated string", `@MCP.Tool("abc)`, "unterminated string"},
		{"missing closing paren", `@MCP.Tool("abc"`, "expected ')'"},
		{"trailing text", `@MCP.Tool("abc") extra`, "unexpected text"},
		{"text after the period", `@MCP.Tool("abc").extra`, "unexpected text"},
		{"empty name", `@MCP.("abc")`, "annotation name"},
		{"missing value", `@MCP.Tool(title=)`, "value"},
		{"missing comma", `@MCP.Tool("a" "b")`, "expected ','"},
		{"double comma", `@MCP.Tool("a",,"b")`, "value"},
		{"bad escape", `@MCP.Tool("a\q")`, "escape"},
		{"bad number", `@MCP.Tool(n=1.2.3)`, "number"},
		{"duplicate key", `@MCP.Tool(title="a", title="b")`, "duplicate"},
		{"key without equals value type", `@MCP.Tool(=1)`, "value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok, err := annotation.ParseLine(tt.line)
			require.Error(t, err)
			assert.True(t, ok, "it is an annotation, just a broken one")
			assert.Contains(t, err.Error(), tt.contains)

			var perr *annotation.Error
			require.ErrorAs(t, err, &perr)
			assert.GreaterOrEqual(t, perr.Column, 1)
		})
	}
}
