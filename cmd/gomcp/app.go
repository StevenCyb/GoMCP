package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/StevenCyb/GoCLI/pkg/cli"
	"github.com/StevenCyb/GoMCP/internal/caller"
	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/StevenCyb/GoMCP/internal/scaffold"
	"github.com/StevenCyb/GoMCP/internal/tui"
)

// linkVersion is overridden at release build time via -ldflags "-X main.linkVersion=...".
var linkVersion = "dev" //nolint:gochecknoglobals // set with -ldflags -X at link time

// version is the link-time version, or the module version recorded by "go install ...@vX.Y.Z".
var version = resolveVersion(linkVersion, buildInfo())

func buildInfo() *debug.BuildInfo {
	info, _ := debug.ReadBuildInfo()
	return info
}

// pseudoVersion matches the versions Go makes up for untagged commits, e.g. v0.0.0-20261114195529-911b73f51138.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}`)

// resolveVersion prefers the version set at link time, then the tagged module version of the build, then "dev".
func resolveVersion(linked string, info *debug.BuildInfo) string {
	if linked != "dev" || info == nil {
		return linked
	}
	if v := info.Main.Version; strings.HasPrefix(v, "v") && !pseudoVersion.MatchString(v) && !strings.Contains(v, "+dirty") {
		return v
	}
	return linked
}

const (
	targetHelp = "A project directory (built and run over stdio) or the URL of a remote server."
	// The flags are read by parseClient, they are described here for the help text.
	clientFlagsHelp = "Flags: --json (raw result), --token <bearer> (or $GOMCP_TOKEN), --oauth (log in with the browser), " +
		"--client-id <id>, --scope <scopes>, --callback-port <port>, --root <dir> (repeatable, answers roots/list), --tags <tags>, --dotenv <file> (project only)."
)

const banner = `   ____       __  __  ____ ____
  / ___| ___ |  \/  |/ ___|  _ \
 | |  _ / _ \| |\/| | |   | |_) |
 | |_| | (_) | |  | | |___|  __/
  \____|\___/|_|  |_|\____|_|`

const (
	argProject = "project_path"
	argOutput  = "output_path"
	argDir     = "dir"
	argTarget  = "target"
	optTags    = "tags"
	optDotEnv  = "dotenv"
	optAddr    = "addr"
	optModule  = "module"
	optNoGet   = "no-get"
)

// actions are the operations the commands run; tests replace them.
type actions struct {
	build       func(ctx context.Context, dir, out, tags string) error
	serve       func(ctx context.Context, o runner.Options) error
	watch       func(ctx context.Context, o runner.Options) error
	init        func(ctx context.Context, o scaffold.Options) error
	client      func(ctx context.Context, cfg caller.Config, req caller.Request, stdout io.Writer) error
	interactive func(ctx context.Context, cfg caller.Config) error
	stdin       io.Reader // arguments of client call given as "-"
}

func realActions() actions {
	return actions{build: runner.Build, serve: runner.Serve, watch: runner.Watch, init: scaffold.Init, client: caller.Run, interactive: tui.Run, stdin: os.Stdin}
}

// run executes the command line and returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, a actions) int {
	var client clientInput
	args, err := parseClient(args, &client)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	app := newApp(ctx, stdout, stderr, a, &client)
	if _, err := app.RunWith(args); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newApp(ctx context.Context, stdout, stderr io.Writer, a actions, client *clientInput) *cli.CLI {
	options := func(c *cli.Context) runner.Options {
		return runner.Options{
			Dir:    *c.GetArgument(argProject),
			Tags:   *c.GetOption(optTags),
			DotEnv: *c.GetOption(optDotEnv),
			Addr:   *c.GetOption(optAddr),
			Stdout: stdout,
			Stderr: stderr,
		}
	}

	return cli.New(
		cli.Name("gomcp"),
		cli.Description("Annotation-driven MCP server generator for Go."),
		cli.Version(version),
		cli.Banner(banner),
		cli.Stream(stdout, stderr),
		cli.Command(
			"build",
			cli.Description("Builds the project into a single binary."),
			cli.Argument(
				argProject,
				cli.Description("Path to the root of the Go project."),
				cli.Argument(
					argOutput,
					cli.Description("Path of the binary to write."),
					cli.Option(optTags, cli.Short('t'), cli.Default(""), cli.Description("Build tags for go build.")),
					cli.Handler(func(c *cli.Context) error {
						return a.build(ctx, *c.GetArgument(argProject), *c.GetArgument(argOutput), *c.GetOption(optTags))
					}),
				),
			),
		),
		cli.Command(
			"serve",
			cli.Description("Builds and runs the project once."),
			cli.Argument(
				argProject,
				cli.Description("Path to the root of the Go project."),
				cli.Option(optTags, cli.Short('t'), cli.Default(""), cli.Description("Build tags for go build.")),
				cli.Option(optDotEnv, cli.Short('e'), cli.Default(""),
					cli.Description("Path to a .env file for the server process (default: <project_path>/.env).")),
				cli.Option(optAddr, cli.Short('a'), cli.Default(""), cli.Description("Listen address of the server (default 127.0.0.1:8080).")),
				cli.Handler(func(c *cli.Context) error { return a.serve(ctx, options(c)) }),
			),
		),
		cli.Command(
			"stdio",
			cli.Description("Builds and runs the project over stdin and stdout, for clients that start the server themselves."),
			cli.Argument(
				argProject,
				cli.Description("Path to the root of the Go project."),
				cli.Option(optTags, cli.Short('t'), cli.Default(""), cli.Description("Build tags for go build.")),
				cli.Option(optDotEnv, cli.Short('e'), cli.Default(""),
					cli.Description("Path to a .env file for the server process (default: <project_path>/.env).")),
				cli.Handler(func(c *cli.Context) error {
					return a.serve(ctx, runner.Options{
						Dir:    *c.GetArgument(argProject),
						Tags:   *c.GetOption(optTags),
						DotEnv: *c.GetOption(optDotEnv),
						Stdio:  true,
						Stdout: stdout,
						Stderr: stderr,
					})
				}),
			),
		),
		cli.Command(
			"client",
			cli.Description("A terminal MCP client for a local project or a remote server, to try tools without an AI client."),
			cli.Command(
				subList,
				cli.Description("Lists the tools of the target. "+clientFlagsHelp),
				cli.Example("gomcp client list ./examples/todo\ngomcp client list https://example.com/mcp --oauth"),
				cli.Argument(argTarget, cli.Description(targetHelp), cli.Handler(func(*cli.Context) error {
					return a.client(ctx, client.config(stderr), caller.Request{}, stdout)
				})),
			),
			cli.Command(
				subCall,
				cli.Description("Calls a tool and prints the result: client call <target> <tool> [JSON object, - reads stdin]. "+clientFlagsHelp),
				cli.Example("gomcp client call ./examples/todo add_todo '{\"title\":\"milk\"}'\ngomcp client call https://example.com/mcp whoami --token $TOKEN"),
				cli.Argument(argTarget, cli.Description(targetHelp), cli.Handler(func(*cli.Context) error {
					req, err := client.resolve(a.stdin)
					if err != nil {
						return err
					}
					return a.client(ctx, client.config(stderr), req, stdout)
				})),
			),
			cli.Command(
				subInteractive,
				cli.Description("Opens a terminal UI: pick a tool, fill in a form, see the result. "+clientFlagsHelp),
				cli.Example("gomcp client interactive ./examples/todo\ngomcp client interactive https://example.com/mcp --oauth"),
				cli.Argument(argTarget, cli.Description(targetHelp), cli.Handler(func(*cli.Context) error {
					return a.interactive(ctx, client.config(io.Discard))
				})),
			),
		),
		cli.Command(
			"watch",
			cli.Description("Runs the project and restarts it on every change."),
			cli.Argument(
				argProject,
				cli.Description("Path to the root of the Go project."),
				cli.Option(optTags, cli.Short('t'), cli.Default(""), cli.Description("Build tags for go build.")),
				cli.Option(optDotEnv, cli.Short('e'), cli.Default(""),
					cli.Description("Path to a .env file for the server process (default: <project_path>/.env).")),
				cli.Option(optAddr, cli.Short('a'), cli.Default(""), cli.Description("Listen address of the server (default 127.0.0.1:8080).")),
				cli.Handler(func(c *cli.Context) error { return a.watch(ctx, options(c)) }),
			),
		),
		cli.Command(
			"init",
			cli.Description("Creates a new GoMCP project with one example tool."),
			cli.Argument(
				argDir,
				cli.Description("Directory to create; it must not contain files."),
				cli.Option(optModule, cli.Short('m'), cli.Default(""), cli.Description("Go module path (default example.com/<directory name>).")),
				cli.Option(optNoGet, cli.Default("false"), cli.Description("Only write the files, skip go get and go mod tidy.")),
				cli.Handler(func(c *cli.Context) error {
					return a.init(ctx, scaffold.Options{
						Dir:     *c.GetArgument(argDir),
						Module:  *c.GetOption(optModule),
						Version: version,
						NoGet:   *c.GetOption(optNoGet) != "false", // a bare flag has an empty value
						Stdout:  stdout,
						Stderr:  stderr,
					})
				}),
			),
		),
		cli.Command(
			"version",
			cli.Description("Prints the version."),
			cli.Handler(func(*cli.Context) error {
				_, err := fmt.Fprintln(stdout, version)
				return err
			}),
		),
	)
}
