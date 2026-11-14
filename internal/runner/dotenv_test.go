package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDotEnv(t *testing.T) {
	content := `# a comment
FOO=bar
export EXPORTED=yes
SPACED = value with spaces
QUOTED="double quoted # not a comment"
SINGLE='single $quoted'
ESCAPED="line1\nline2 \"q\""
INLINE=value # trailing comment
EMPTY=
   INDENTED=ok

=novalue
NOEQUALS
`
	got, err := runner.ParseDotEnv(content)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"FOO":      "bar",
		"EXPORTED": "yes",
		"SPACED":   "value with spaces",
		"QUOTED":   "double quoted # not a comment",
		"SINGLE":   "single $quoted",
		"ESCAPED":  "line1\nline2 \"q\"",
		"INLINE":   "value",
		"EMPTY":    "",
		"INDENTED": "ok",
	}, got)
}

func TestParseDotEnvUnterminatedQuote(t *testing.T) {
	_, err := runner.ParseDotEnv("A=\"open\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1")
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	env, err := runner.LoadDotEnv(filepath.Join(t.TempDir(), ".env"), []string{"A=1"})
	require.NoError(t, err, "a missing .env is fine")
	assert.Equal(t, []string{"A=1"}, env)
}

func TestLoadDotEnvDoesNotOverrideTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("A=from_file\nB=from_file\n"), 0o600))

	env, err := runner.LoadDotEnv(path, []string{"A=from_env", "C=1"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"A=from_env", "C=1", "B=from_file"}, env)
}

func TestLoadDotEnvSyntaxError(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("A=\"open\n"), 0o600))
	_, err := runner.LoadDotEnv(path, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}
