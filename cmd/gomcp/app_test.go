package main

import (
	"bytes"
	"context"
	"errors"
	"runtime/debug"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/StevenCyb/GoMCP/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cmdServe = "serve"

// fakeActions records the calls the CLI makes.
type fakeActions struct {
	calls []string
	build struct{ dir, out, tags string }
	opts  runner.Options
	scaf  scaffold.Options
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
		init: func(_ context.Context, o scaffold.Options) error {
			f.calls = append(f.calls, "init")
			f.scaf = o
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

func TestStdioCommand(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "stdio", "./example", "--tags", "dev", "--dotenv", "x.env")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{cmdServe}, f.calls, "stdio runs the server like serve does, over stdin and stdout")
	assert.True(t, f.opts.Stdio)
	assert.Equal(t, "./example", f.opts.Dir)
	assert.Equal(t, "dev", f.opts.Tags)
	assert.Equal(t, "x.env", f.opts.DotEnv)
	assert.Empty(t, f.opts.Addr, "there is no listen address over stdio")
}

func TestStdioCommandHasNoAddressFlag(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "stdio", "./example", "--addr", ":1")
	if code == 0 { // the command line library may ignore unknown flags
		assert.Empty(t, f.opts.Addr)
	} else {
		assert.NotEmpty(t, stderr)
	}
}

func TestServeIsNotStdio(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdServe, "./example")
	require.Equal(t, 0, code, stderr)
	assert.False(t, f.opts.Stdio)
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

func TestInit(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "init", "./notes")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{"init"}, f.calls)
	assert.Equal(t, "./notes", f.scaf.Dir)
	assert.Empty(t, f.scaf.Module)
	assert.False(t, f.scaf.NoGet)
	assert.Equal(t, version, f.scaf.Version)
}

func TestInitFlags(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "init", "./notes", "--module", "example.org/notes", "--no-get")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "example.org/notes", f.scaf.Module)
	assert.True(t, f.scaf.NoGet)
}

func TestInitErrorFailsTheCommand(t *testing.T) {
	f := &fakeActions{err: errors.New("not empty")}
	code, _, stderr := execute(t, f, "init", "./notes")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not empty")
}

func TestResolveVersion(t *testing.T) {
	build := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	assert.Equal(t, "v1.2.3", resolveVersion("v1.2.3", build("v9.9.9")), "a version set at link time wins")
	assert.Equal(t, "v0.1.0", resolveVersion("dev", build("v0.1.0")), "go install records the module version")
	assert.Equal(t, "v0.2.0-rc1", resolveVersion("dev", build("v0.2.0-rc1")), "pre-release tags are real versions")
	assert.Equal(t, "dev", resolveVersion("dev", build("(devel)")), "a local build has no version")
	assert.Equal(t, "dev", resolveVersion("dev", build("v0.0.0-20261114195529-911b73f51138+dirty")), "untagged commits are dev")
	assert.Equal(t, "dev", resolveVersion("dev", build("v0.1.1-0.20261114195529-911b73f51138")), "pseudo-versions are dev")
	assert.Equal(t, "dev", resolveVersion("dev", build("")))
	assert.Equal(t, "dev", resolveVersion("dev", nil), "no build info at all")
}
