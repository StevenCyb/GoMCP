// Package runner builds, runs and watches an annotated module: it parses the annotations,
// generates the server's main package and compiles it with an overlay, so nothing is
// written into the user's module.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/StevenCyb/GoMCP/internal/generator"
	"github.com/StevenCyb/GoMCP/internal/parser"
)

// Options configure Serve and Watch.
type Options struct {
	Dir    string // the module (or a directory inside it)
	Tags   string // build tags
	DotEnv string // .env file; default: <module>/.env
	Stdout io.Writer
	Stderr io.Writer
	// Stdio runs the server over stdin and stdout instead of HTTP (GOMCP_TRANSPORT=stdio). Stdout then
	// carries protocol messages only, gomcp logs to stderr. Addr is ignored and Watch refuses it.
	Stdio bool
	Stdin io.Reader // input of a stdio server; default os.Stdin
	// Addr overrides the listen address of the server (GOMCP_ADDR).
	Addr string
}

// errWatchStdio is returned when watch is asked to serve over stdio: restarting would drop the client's connection.
var errWatchStdio = errors.New("watch cannot serve over stdio: a restart would end the client connection")

func logf(format string, args ...any) { log.Printf("[gomcp] "+format, args...) }

func (o Options) stdout() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return os.Stdout
}

func (o Options) stdin() io.Reader {
	if o.Stdin != nil {
		return o.Stdin
	}
	return os.Stdin
}

func (o Options) stderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return os.Stderr
}

// prepared is a parsed project with its generated main package in a temporary directory.
type prepared struct {
	moduleDir string
	tmp       string
	overlay   string
}

// prepare parses the annotations and generates the server's main package.
func prepare(dir string) (*prepared, error) {
	p, err := parser.Parse(dir)
	if err != nil {
		return nil, err
	}
	src, err := generator.Generate(p)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	tmp, err := os.MkdirTemp("", "gomcp-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	pr := &prepared{moduleDir: p.Module.Dir, tmp: tmp, overlay: filepath.Join(tmp, "overlay.json")}

	mainFile := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(mainFile, src, 0o600); err != nil {
		pr.cleanup()
		return nil, fmt.Errorf("write generated code: %w", err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{
		"Replace": {filepath.Join(p.Module.Dir, ".gomcp", "main.go"): mainFile},
	})
	if err != nil {
		pr.cleanup()
		return nil, fmt.Errorf("encode overlay: %w", err)
	}
	if err := os.WriteFile(pr.overlay, overlay, 0o600); err != nil {
		pr.cleanup()
		return nil, fmt.Errorf("write overlay: %w", err)
	}
	return pr, nil
}

func (pr *prepared) cleanup() { _ = os.RemoveAll(pr.tmp) }

// compile builds the generated package into the binary out.
func (pr *prepared) compile(ctx context.Context, out, tags string) error {
	args := []string{"build", "-overlay", pr.overlay, "-o", out}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./.gomcp")
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed command, arguments are paths we created
	cmd.Dir = pr.moduleDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, output)
	}
	return nil
}

// Build parses the module and compiles the server into the binary out.
func Build(ctx context.Context, dir, out, tags string) error {
	pr, err := prepare(dir)
	if err != nil {
		return err
	}
	defer pr.cleanup()
	absOut, err := filepath.Abs(out)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", out, err)
	}
	return pr.compile(ctx, absOut, tags)
}

var errNoModule = errors.New("module not found")

// exeSuffix is the file extension executables need on this OS.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// build prepares and compiles into a numbered binary in a scratch directory.
func build(ctx context.Context, o Options, scratch string, n int) (string, error) {
	pr, err := prepare(o.Dir)
	if err != nil {
		return "", err
	}
	defer pr.cleanup()
	bin := filepath.Join(scratch, "server-"+strconv.Itoa(n)+exeSuffix())
	if err := pr.compile(ctx, bin, o.Tags); err != nil {
		return "", err
	}
	return bin, nil
}

// environment is the process environment plus the .env file.
func environment(o Options) ([]string, error) {
	path := o.DotEnv
	if path == "" {
		root, _, err := parser.FindModule(o.Dir)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errNoModule, err)
		}
		path = filepath.Join(root, ".env")
	}
	env, err := LoadDotEnv(path, os.Environ())
	if err != nil {
		return nil, err
	}
	switch {
	case o.Stdio:
		env = append(env, "GOMCP_TRANSPORT=stdio") // the last entry wins
	case o.Addr != "":
		env = append(env, "GOMCP_ADDR="+o.Addr)
	}
	return env, nil
}
