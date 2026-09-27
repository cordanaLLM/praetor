package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const lspVersion = "v1.0.0"

// main wires the daemon to SIGINT/SIGTERM. The entry point owns the process lifetime, so
// the daemon's root context is built here (HISS-02): the signal cancels it, which
// Server.Run observes even while it idles on stdin, so a signalled daemon exits promptly
// with status 0. os.Exit runs only after run returned and the signal handler was released.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx)
	stop()
	os.Exit(code)
}

// run serves the language server protocol on stdio until ctx is cancelled or stdin ends.
func run(ctx context.Context) int {
	srv := NewServer(os.Stdin, os.Stdout, lspVersion)
	err := srv.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	fmt.Fprintf(os.Stderr, "standards-lsp daemon error: %v\n", err)
	return 1
}
