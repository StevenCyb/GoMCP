package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mainEnv = "GOMCP_TEST_RUN_MAIN"

// TestMainProcess runs main in a subprocess (it ends in os.Exit) and checks output and exit code.
func TestMainProcess(t *testing.T) {
	if args := os.Getenv(mainEnv); args != "" {
		os.Args = append([]string{"gomcp"}, strings.Fields(args)...)
		main()
		return
	}

	tests := []struct {
		name     string
		args     string
		wantCode int
		wantOut  string
	}{
		{"version", "version", 0, version},
		{"unknown command", "deploy", 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainProcess$")
			cmd.Env = append(os.Environ(), mainEnv+"="+tt.args)
			out, err := cmd.CombinedOutput()
			code := 0
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exitErr.ExitCode()
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantCode, code, string(out))
			assert.Contains(t, string(out), tt.wantOut)
		})
	}
}

func TestRealActionsAreWiredToTheRunner(t *testing.T) {
	a := realActions()
	require.NotNil(t, a.build)
	require.NotNil(t, a.serve)
	require.NotNil(t, a.watch)

	// an empty directory is no module, so the real build must fail instead of doing nothing
	err := a.build(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "out"), "")
	require.Error(t, err)
}
