package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/StevenCyb/GoCLI/pkg/cli"
	"github.com/StevenCyb/GoMCP/internal/runner"
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

const banner = `   ____       __  __  ____ ____
  / ___| ___ |  \/  |/ ___|  _ \
 | |  _ / _ \| |\/| | |   | |_) |
 | |_| | (_) | |  | | |___|  __/
  \____|\___/|_|  |_|\____|_|`

const (
	argProject = "project_path"
	argOutput  = "output_path"
	optTags    = "tags"
	optDotEnv  = "dotenv"
	optAddr    = "addr"
)

// actions are the operations the commands run; tests replace them.
type actions struct {
	build func(ctx context.Context, dir, out, tags string) error
	serve func(ctx context.Context, o runner.Options) error
	watch func(ctx context.Context, o runner.Options) error
}

func realActions() actions {
	return actions{build: runner.Build, serve: runner.Serve, watch: runner.Watch}
}

// run executes the command line and returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, a actions) int {
	app := newApp(ctx, stdout, stderr, a)
	if _, err := app.RunWith(args); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newApp(ctx context.Context, stdout, stderr io.Writer, a actions) *cli.CLI {
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
			"version",
			cli.Description("Prints the version."),
			cli.Handler(func(*cli.Context) error {
				_, err := fmt.Fprintln(stdout, version)
				return err
			}),
		),
	)
}
