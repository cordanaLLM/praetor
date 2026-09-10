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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := NewServer(os.Stdin, os.Stdout, lspVersion)
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "standards-lsp daemon error: %v\n", err)
		os.Exit(1)
	}
}
