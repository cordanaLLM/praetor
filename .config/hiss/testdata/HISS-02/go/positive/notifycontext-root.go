package p

import (
	"context"
	"net"
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

func listen(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}
