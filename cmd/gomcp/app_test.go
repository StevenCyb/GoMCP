package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/StevenCyb/GoMCP/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmdServe    = "serve"
	cmdClient   = "client"
	toolAddNote = "add_note"
	exampleDir  = "./example"
)

// fakeActions records the calls the CLI makes.
type fakeActions struct {
	calls []string
	build struct{ dir, out, tags string }
	opts  runner.Options
	cfg   caller.Config
	scaf  scaffold.Options
	req   caller.Request
	stdin string
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
		client: func(_ context.Context, cfg caller.Config, req caller.Request, stdout io.Writer) error {
			f.calls = append(f.calls, cmdClient)
			f.cfg, f.req = cfg, req
			_, _ = io.WriteString(stdout, "result\n")
			return f.err
		},
		interactive: func(_ context.Context, cfg caller.Config) error {
			f.calls = append(f.calls, subInteractive)
			f.cfg = cfg
			return f.err
		},
		stdin: strings.NewReader(f.stdin),
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
	code, _, stderr := execute(t, f, "build", "./examples/features", "./bin/notes")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{"build"}, f.calls)
	assert.Equal(t, "./examples/features", f.build.dir)
	assert.Equal(t, "./bin/notes", f.build.out)
	assert.Empty(t, f.build.tags)
}

func TestBuildWithTags(t *testing.T) {
	for _, flag := range []string{"--tags", "-t"} {
		f := &fakeActions{}
		code, _, stderr := execute(t, f, "build", "./examples/features", "./out", flag, "integration")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "integration", f.build.tags, flag)
	}
}

func TestServe(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdServe, "./examples/features")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{cmdServe}, f.calls)
	assert.Equal(t, "./examples/features", f.opts.Dir)
	assert.Empty(t, f.opts.Tags)
	assert.Empty(t, f.opts.DotEnv)
	assert.Empty(t, f.opts.Addr)
}

func TestStdioCommand(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "stdio", "./examples/features", "--tags", "dev", "--dotenv", "x.env")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{cmdServe}, f.calls, "stdio runs the server like serve does, over stdin and stdout")
	assert.True(t, f.opts.Stdio)
	assert.Equal(t, "./examples/features", f.opts.Dir)
	assert.Equal(t, "dev", f.opts.Tags)
	assert.Equal(t, "x.env", f.opts.DotEnv)
	assert.Empty(t, f.opts.Addr, "there is no listen address over stdio")
}

func TestStdioCommandHasNoAddressFlag(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, "stdio", "./examples/features", "--addr", ":1")
	if code == 0 { // the command line library may ignore unknown flags
		assert.Empty(t, f.opts.Addr)
	} else {
		assert.NotEmpty(t, stderr)
	}
}

func TestClientListsTools(t *testing.T) {
	f := &fakeActions{}
	code, stdout, stderr := execute(t, f, cmdClient, subList, exampleDir)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{cmdClient}, f.calls)
	assert.Equal(t, "result\n", stdout, "the result is on stdout")
	assert.Equal(t, exampleDir, f.cfg.Target)
	assert.Empty(t, f.req.Tool, "no tool means listing")
}

func TestClientCallATool(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdClient, subCall, exampleDir, toolAddNote, `{"text":"hi"}`)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, toolAddNote, f.req.Tool)
	assert.JSONEq(t, `{"text":"hi"}`, string(f.req.Args))
	assert.False(t, f.req.JSON)
}

func TestClientCallWithoutArguments(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdClient, subCall, exampleDir, "list_notes")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "list_notes", f.req.Tool)
	assert.Empty(t, f.req.Args)
}

func TestClientFlagsInAnyPosition(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdClient, subCall, "--json", "-t", "dev", exampleDir, "-e", "x.env", "get_note",
		"--root", "a", "-r", "b", "--scope=read write", "--client-id", "abc", "--callback-port", "8123", "--token", "tok", "--oauth", `{"id":"1"}`)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, caller.Config{
		Target: exampleDir, Tags: "dev", DotEnv: "x.env", Roots: []string{"a", "b"},
		Token: "tok", OAuth: true, ClientID: "abc", Scope: "read write", CallbackPort: 8123, Stderr: f.cfg.Stderr,
	}, f.cfg)
	assert.Equal(t, "get_note", f.req.Tool)
	assert.True(t, f.req.JSON)
}

func TestClientTokenFromEnvironment(t *testing.T) {
	t.Setenv(envToken, "from-env")
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdClient, subList, "https://example.com/mcp")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "from-env", f.cfg.Token)

	code, _, stderr = execute(t, f, cmdClient, subList, "https://example.com/mcp", "--token", "flag")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "flag", f.cfg.Token, "the flag wins")
}

func TestClientInteractive(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdClient, subInteractive, exampleDir, "--oauth")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, []string{subInteractive}, f.calls)
	assert.Equal(t, exampleDir, f.cfg.Target)
	assert.True(t, f.cfg.OAuth)
}

func TestClientArgumentsFromStdin(t *testing.T) {
	f := &fakeActions{stdin: ` {"text":"from stdin"}` + "\n"}
	code, _, stderr := execute(t, f, cmdClient, subCall, exampleDir, toolAddNote, "-")
	require.Equal(t, 0, code, stderr)
	assert.JSONEq(t, `{"text":"from stdin"}`, string(f.req.Args))
}

func TestClientRejectsBadInputBeforeConnecting(t *testing.T) {
	cases := map[string][]string{
		"not json":          {cmdClient, subCall, exampleDir, toolAddNote, "{text"},
		"not an object":     {cmdClient, subCall, exampleDir, toolAddNote, "[1]"},
		"bad stdin":         {cmdClient, subCall, exampleDir, toolAddNote, "-"},
		"unknown flag":      {cmdClient, subList, exampleDir, "--nope"},
		"missing value":     {cmdClient, subList, exampleDir, "--root"},
		"too many":          {cmdClient, subCall, exampleDir, toolAddNote, "{}", "extra"},
		"call without tool": {cmdClient, subCall, exampleDir},
		"list with tool":    {cmdClient, subList, exampleDir, toolAddNote},
		"no target":         {cmdClient, subList},
		"no subcommand":     {cmdClient},
		"unknown sub":       {cmdClient, "nope", exampleDir},
		"old call command":  {"call", exampleDir},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeActions{stdin: "nope"}
			code, stdout, stderr := execute(t, f, args...)
			assert.Equal(t, 1, code)
			assert.NotEmpty(t, stderr)
			assert.Empty(t, stdout)
			assert.Empty(t, f.calls, "nothing is built")
		})
	}
}

func TestClientFailureExitsWithOneAndMessageOnStderr(t *testing.T) {
	f := &fakeActions{err: &caller.ToolError{Message: "it broke"}}
	code, _, stderr := execute(t, f, cmdClient, subCall, exampleDir, "fail")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "it broke")
}

func TestServeIsNotStdio(t *testing.T) {
	f := &fakeActions{}
	code, _, stderr := execute(t, f, cmdServe, "./examples/features")
	require.Equal(t, 0, code, stderr)
	assert.False(t, f.opts.Stdio)
}

func TestServeAndWatchFlags(t *testing.T) {
	for _, command := range []string{cmdServe, "watch"} {
		t.Run(command, func(t *testing.T) {
			f := &fakeActions{}
			code, _, stderr := execute(t, f, command, "./examples/features", "-t", "dev", "-e", "local.env", "-a", ":9000")
			require.Equal(t, 0, code, stderr)
			assert.Equal(t, []string{command}, f.calls)
			assert.Equal(t, "dev", f.opts.Tags)
			assert.Equal(t, "local.env", f.opts.DotEnv)
			assert.Equal(t, ":9000", f.opts.Addr)

			f = &fakeActions{}
			code, _, stderr = execute(t, f, command, "./examples/features", "--tags", "dev", "--dotenv", "local.env", "--addr", ":9001")
			require.Equal(t, 0, code, stderr)
			assert.Equal(t, ":9001", f.opts.Addr)
			assert.Equal(t, "local.env", f.opts.DotEnv)
		})
	}
}

func TestOutputStreamsAreForwarded(t *testing.T) {
	f := &fakeActions{}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"gomcp", cmdServe, "./examples/features"}, &stdout, &stderr, f.actions())
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
		"build needs paths": {"build", "./examples/features"},
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
