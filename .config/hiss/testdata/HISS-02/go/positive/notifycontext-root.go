package p

import (
	"context"
	"os"
	"os/signal"
)

// A signal cancels this context, but nothing bounds it when no signal arrives. Outside
// main.main a library does not own the process lifetime.
func Serve(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return listen(ctx, addr)
}

func listen(ctx context.Context, addr string) error { return ctx.Err() }
