// Command gomcp builds and runs MCP servers from annotated Go code.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args, os.Stdout, os.Stderr, realActions())
	stop()
	os.Exit(code)
}
