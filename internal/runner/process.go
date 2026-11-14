package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const stopTimeout = 10 * time.Second

// process is a running server.
type process struct {
	cmd  *exec.Cmd
	done chan struct{} // closed when the process exited
	err  error         // exit error, valid once done is closed
}

func start(ctx context.Context, bin string, env []string, o Options) (*process, error) {
	cmd := exec.CommandContext(ctx, bin)
	// when ctx ends, ask the server to shut down gracefully and kill it only after a delay
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = stopTimeout
	cmd.Env = env
	cmd.Stdout = o.stdout()
	cmd.Stderr = o.stderr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// stop asks the server to shut down and kills it if it does not.
func (p *process) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// Serve builds the server and runs it until ctx is cancelled or the server exits.
func Serve(ctx context.Context, o Options) error {
	scratch, err := os.MkdirTemp("", "gomcp-run-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	env, err := environment(o)
	if err != nil {
		return err
	}
	bin, err := build(ctx, o, scratch, 1)
	if err != nil {
		return err
	}
	proc, err := start(ctx, bin, env, o)
	if err != nil {
		return err
	}
	logf("started (pid %d)", proc.cmd.Process.Pid)

	select {
	case <-ctx.Done():
		proc.stop()
		logf("stopped")
		return nil
	case <-proc.done:
		if err := proc.err; err != nil {
			if _, ok := errors.AsType[*exec.ExitError](err); ok {
				return fmt.Errorf("server exited: %w", err)
			}
			return fmt.Errorf("server: %w", err)
		}
		return nil
	}
}
