// Package scaffold writes the files of a new GoMCP project.
package scaffold

import (
	"context"
	_ "embed" // templates
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// GoVersion is the "go" line of generated projects; a test keeps it equal to the repo's go.mod.
const GoVersion = "1.26.2"

//go:embed templates/api.go.tmpl
var apiTmpl string

//go:embed templates/README.md.tmpl
var readmeTmpl string

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Options configure Init.
type Options struct {
	Dir     string // directory to create
	Module  string // module path, default example.com/<basename of Dir>
	Version string // GoMCP version for go get; "dev" or "" means latest
	NoGet   bool   // skip go get and go mod tidy
	Stdout  io.Writer
	Stderr  io.Writer
}

// Render returns the project files by name. The name is the server name and must not contain path separators.
func Render(name, module string) (map[string]string, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid project name %q: use letters, digits, '.', '_' and '-'", name)
	}
	if module == "" || strings.ContainsAny(module, " \t\r\n\\") {
		return nil, fmt.Errorf("invalid module path %q", module)
	}
	files := map[string]string{
		"go.mod":     "module " + module + "\n\ngo " + GoVersion + "\n",
		".gitignore": ".env\n",
	}
	for file, src := range map[string]string{"api.go": apiTmpl, "README.md": readmeTmpl} {
		var b strings.Builder
		if err := template.Must(template.New(file).Parse(src)).Execute(&b, struct{ Name string }{name}); err != nil {
			return nil, fmt.Errorf("render %s: %w", file, err)
		}
		files[file] = b.String()
	}
	return files, nil
}

// Init creates the project directory, writes the files and, unless NoGet is set, fetches GoMCP.
func Init(ctx context.Context, o Options) error {
	abs, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	name := filepath.Base(abs)
	module := o.Module
	if module == "" {
		module = "example.com/" + name
	}
	files, err := Render(name, module)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty, refusing to write into it", o.Dir)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return err
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(abs, f), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", f, err)
		}
	}
	out := o.Stdout
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "Created %s (module %s)\n", o.Dir, module)

	if o.NoGet {
		_, _ = fmt.Fprintf(out, "\nNext:\n  cd %s\n  go get %s\n  go mod tidy\n  gomcp serve .\n", o.Dir, dependency(o.Version))
		return nil
	}
	if err := fetch(ctx, abs, o); err != nil {
		return fmt.Errorf("%w\nThe files are written in %s. To finish: cd %s && go get %s && go mod tidy",
			err, o.Dir, o.Dir, dependency(o.Version))
	}
	_, _ = fmt.Fprintf(out, "\nNext:\n  cd %s\n  gomcp serve .\n\nStdio client config: see %s\n", o.Dir, filepath.Join(o.Dir, "README.md"))
	return nil
}

func dependency(version string) string {
	if version == "" || version == "dev" {
		version = "latest"
	}
	return "github.com/StevenCyb/GoMCP@" + version
}

func fetch(ctx context.Context, dir string, o Options) error {
	for _, args := range [][]string{{"get", dependency(o.Version)}, {"mod", "tidy"}} {
		cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed go subcommands, version comes from the build
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, o.Stdout, o.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}
