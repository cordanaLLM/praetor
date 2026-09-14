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

func main() {
	os.Exit(run())
}

// run wires the daemon to stdio and to SIGINT/SIGTERM. The signal cancels the run
// context, which Server.Run observes even while it idles on stdin, so a signalled
// daemon exits promptly with status 0; os.Exit happens only after every defer ran.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := NewServer(os.Stdin, os.Stdout, lspVersion)
	err := srv.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	fmt.Fprintf(os.Stderr, "standards-lsp daemon error: %v\n", err)
	return 1
}
