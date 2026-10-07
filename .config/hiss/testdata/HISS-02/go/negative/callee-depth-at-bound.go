package wol

import (
	"context"
	"net"
	"time"
)

// The context passes through three module-local calls and the fourth derives the deadline:
// exactly the depth the callee walk follows (maxCalleeDepth = 4), so the call is bounded.
func Wake(addr string) error {
	return send(context.Background(), addr)
}

func send(ctx context.Context, addr string) error { return resolve(ctx, addr) }

func resolve(ctx context.Context, addr string) error { return open(ctx, addr) }

func open(ctx context.Context, addr string) error { return dial(ctx, addr) }

func dial(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
