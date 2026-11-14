package runner

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StevenCyb/GoMCP/internal/parser"
)

const defaultPoll = 500 * time.Millisecond

type fileState struct {
	mod  time.Time
	size int64
}

// snapshot records the files whose change should restart the server.
func snapshot(root string) map[string]fileState {
	out := map[string]fileState{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable files are simply not watched
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && parser.SkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		watched := (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) ||
			name == "go.mod" || name == "go.sum" || name == ".env"
		if !watched {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			out[path] = fileState{mod: info.ModTime(), size: info.Size()}
		}
		return nil
	})
	return out
}

// Watch builds and runs the server and rebuilds it whenever a source file changes, until
// ctx is cancelled. A failed rebuild keeps the previous server running.
func Watch(ctx context.Context, o Options) error {
	root, _, err := parser.FindModule(o.Dir)
	if err != nil {
		return fmt.Errorf("%w: %w", errNoModule, err)
	}
	scratch, err := os.MkdirTemp("", "gomcp-watch-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	var (
		current *process
		builds  int
	)
	defer func() {
		if current != nil {
			current.stop()
		}
	}()

	rebuild := func() {
		builds++
		logf("building...")
		bin, err := build(ctx, o, scratch, builds)
		if err != nil {
			logf("build failed:\n%v", err)
			return
		}
		env, err := environment(o)
		if err != nil {
			logf("build failed: %v", err)
			return
		}
		if current != nil {
			current.stop()
			current = nil
		}
		proc, err := start(ctx, bin, env, o)
		if err != nil {
			logf("%v", err)
			return
		}
		current = proc
		logf("started (pid %d)", proc.cmd.Process.Pid)
	}

	last := snapshot(root)
	rebuild()

	ticker := time.NewTicker(defaultPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("shutting down")
			return nil
		case <-ticker.C:
			now := snapshot(root)
			if maps.Equal(now, last) {
				continue
			}
			// let the editor finish writing before building
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(defaultPoll):
			}
			last = snapshot(root)
			rebuild()
		}
	}
}
