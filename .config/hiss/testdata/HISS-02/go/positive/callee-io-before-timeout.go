package pgx

import (
	"context"
	"net"
	"time"
)

// The near miss: the callee derives a timeout, but only after it has already dialed with the
// caller's deadline-free context, so the first dial is unbounded.
func Open(addr string) error {
	return connect(context.Background(), addr, time.Second)
}

func connect(ctx context.Context, addr string, timeout time.Duration) error {
	var d net.Dialer
	probe, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if err := probe.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
