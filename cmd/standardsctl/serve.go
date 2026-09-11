package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/standards/internal/container"
	"github.com/cordanaLLM/standards/internal/hiss"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "HTTP address to bind health and liveness probes")
	drain := fs.Duration("drain-timeout", 10*time.Second, "Graceful drain timeout on SIGTERM/SIGINT")
	auditInterval := fs.Duration("audit-interval", 0, "Periodic background HISS audit interval (0 to disable)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hs := container.NewHealthServer(*addr)
	if err := hs.Start(); err != nil {
		return fmt.Errorf("failed starting health server on %s: %w", *addr, err)
	}
	fmt.Printf("=== Praetor Cloud-Native Container Sentinel Running ===\n")
	fmt.Printf("HTTP Probes listening on %s (/healthz, /livez, /readyz)\n", *addr)

	if *auditInterval > 0 {
		startPeriodicAudit(ctx, *auditInterval, hs)
	}

	if err := container.WaitForGracefulDrain(ctx, hs, *drain); err != nil {
		return fmt.Errorf("server drain error: %w", err)
	}
	fmt.Printf("Praetor sentinel stopped gracefully.\n")
	return nil
}

func startPeriodicAudit(ctx context.Context, interval time.Duration, hs *container.HealthServer) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if ctx.Err() != nil {
				return
			}
			runAuditCheck(ctx, hs)
		}
	}()
}

func runAuditCheck(ctx context.Context, hs *container.HealthServer) {
	opts := hiss.ScanOptions{Cap: 500, MaxFuncLOC: 60}
	rep, err := hiss.Scan(ctx, ".", opts)
	if err != nil || rep.TotalInfractions > 0 {
		hs.SetReady(false)
		return
	}
	hs.SetReady(true)
}
