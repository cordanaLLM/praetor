package p

import (
	"context"
	"net"
	"os/exec"
	"time"
)

// The context-aware forms carry the caller's deadline; DialTimeout carries its own.
func Probe(ctx context.Context, addr string) error {
	if err := exec.CommandContext(ctx, "git", "status").Run(); err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	other, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	return other.Close()
}
