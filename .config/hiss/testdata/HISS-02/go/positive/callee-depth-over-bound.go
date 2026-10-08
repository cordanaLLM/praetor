package wol

import (
	"context"
	"net"
	"time"
)

// One call deeper than the callee walk follows (maxCalleeDepth = 4): the deadline is derived
// five calls down, so the walk cannot prove it and the call is reported.
func Wake(addr string) error {
	return send(context.Background(), addr)
}

func send(ctx context.Context, addr string) error { return resolve(ctx, addr) }

func resolve(ctx context.Context, addr string) error { return route(ctx, addr) }

func route(ctx context.Context, addr string) error { return open(ctx, addr) }

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
