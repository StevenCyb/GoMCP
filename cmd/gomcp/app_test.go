package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cmdServe = "serve"

// fakeActions records the calls the CLI makes.
type fakeActions struct {
	calls []string
	build struct{ dir, out, tags string }
	opts  runner.Options
	err   error
}

func (f *fakeActions) actions() actions {
	return actions{
		build: func(_ context.Context, dir, out, tags string) error {
			f.calls = append(f.calls, "build")
			f.build.dir, f.build.out, f.build.tags = dir, out, tags
			return f.err
		},
		serve: func(_ context.Context, o runner.Options) error {
			f.calls = append(f.calls, "serve")
			f.opts = o
			return f.err
		},
		watch: func(_ context.Context, o runner.Options) error {
			f.calls = append(f.calls, "watch")
			f.opts = o
			return f.err
		},
	}
}

func execute(t *testing.T, f *fakeActions, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), append([]string{"gomcp"}, args...), &stdout, &stderr, f.actions())
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	f := &fakeActions{}
	code, stdout, _ := execute(t, f, "version")
	assert.Equal(t, 0, code)
	assert.Equal(t, version+"\n", stdout)
	assert.Empty(t, f.calls)
}

func TestBuild(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "build", "./example", "./bin/notes")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{"build"}, f.calls)
	assert.Equal(t, "./example", f.build.dir)
	assert.Equal(t, "./bin/notes", f.build.out)
	assert.Empty(t, f.build.tags)
}

func TestBuildWithTags(t *testing.T) {
	for _, flag := range []string{"--tags", "-t"} {
		f := &fakeActions{}
		code, _, stderr := execute(t, f, "build", "./example", "./out", flag, "integration")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "integration", f.build.tags, flag)
	}
}

func TestServe(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdServe, "./example")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{cmdServe}, f.calls)
	assert.Equal(t, "./example", f.opts.Dir)
	assert.Empty(t, f.opts.Tags)
	assert.Empty(t, f.opts.DotEnv)
	assert.Empty(t, f.opts.Addr)
}

func TestServeAndWatchFlags(t *testing.T) {
	for _, command := range []string{cmdServe, "watch"} {
		t.Run(command, func(t *testing.T) {
			f := &fakeActions{}
			code, _, stderr := execute(t, f, command, "./example", "-t", "dev", "-e", "local.env", "-a", ":9000")
			require.Equal(t, 0, code, stderr)
			assert.Equal(t, []string{command}, f.calls)
			assert.Equal(t, "dev", f.opts.Tags)
			assert.Equal(t, "local.env", f.opts.DotEnv)
			assert.Equal(t, ":9000", f.opts.Addr)

			f = &fakeActions{}
			code, _, stderr = execute(t, f, command, "./example", "--tags", "dev", "--dotenv", "local.env", "--addr", ":9001")
			require.Equal(t, 0, code, stderr)
			assert.Equal(t, ":9001", f.opts.Addr)
			assert.Equal(t, "local.env", f.opts.DotEnv)
		})
	}
}

func TestOutputStreamsAreForwarded(t *testing.T) {
	f := &fakeActions{}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"gomcp", cmdServe, "./example"}, &stdout, &stderr, f.actions())
	require.Equal(t, 0, code)
	assert.Same(t, &stdout, f.opts.Stdout)
	assert.Same(t, &stderr, f.opts.Stderr)
}

func TestActionErrorsFailTheCommand(t *testing.T) {
	for _, args := range [][]string{{"build", "./e", "./o"}, {cmdServe, "./e"}, {"watch", "./e"}} {
		f := &fakeActions{err: errors.New("annotation problem")}
		code, _, stderr := execute(t, f, args...)
		assert.Equal(t, 1, code, args)
		assert.Contains(t, stderr, "annotation problem")
	}
}

func TestUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"unknown command":   {"deploy"},
		"no command":        {},
		"build needs paths": {"build", "./example"},
		"serve needs path":  {cmdServe},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeActions{}
			code, _, stderr := execute(t, f, args...)
			assert.Equal(t, 1, code)
			assert.NotEmpty(t, stderr)
			assert.Empty(t, f.calls, "nothing runs on a usage error")
		})
	}
}
