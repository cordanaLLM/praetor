package p

import (
	"context"
	"net"
)

// WithoutCancel drops the caller's deadline along with its cancellation, so the cleanup
// can hang after the caller has given up.
func Upload(ctx context.Context, addr string) error {
	if err := send(ctx, addr); err != nil {
		return remove(context.WithoutCancel(ctx), addr)
	}
	return nil
}

func send(ctx context.Context, addr string) error   { return dial(ctx, addr) }
func remove(ctx context.Context, addr string) error { return dial(ctx, addr) }

func dial(ctx context.Context, addr string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
