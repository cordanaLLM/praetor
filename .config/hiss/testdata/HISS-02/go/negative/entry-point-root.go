package main

import (
	"context"
	"os"
	"os/signal"
)

// main.main owns the process lifetime, and the root context of a long-running server is
// that lifetime.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(ctx); err != nil {
		os.Exit(1)
	}
}

func serve(ctx context.Context) error { return ctx.Err() }
