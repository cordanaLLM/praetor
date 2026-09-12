package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/container"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// maxAuditTicks is the scalar upper bound (HISS-02) on the number of periodic audit
// rounds a single serve process performs. At the smallest sensible interval this is
// years of uptime; it exists so the loop is statically bounded.
const maxAuditTicks = 1 << 24

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "HTTP address to bind health and liveness probes")
	drain := fs.Duration("drain-timeout", 10*time.Second, "Graceful drain timeout on SIGTERM/SIGINT")
	auditInterval := fs.Duration("audit-interval", 0, "Periodic background HISS audit interval (0 to disable)")
	auditPath := fs.String("audit-path", ".", "Repository path scanned by the periodic HISS audit")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *drain <= 0 {
		return fmt.Errorf("--drain-timeout must be positive, got %s", *drain)
	}
	if *auditInterval < 0 {
		return fmt.Errorf("--audit-interval must not be negative, got %s", *auditInterval)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hs := container.NewHealthServer(*addr)
	if err := hs.Start(ctx); err != nil {
		return fmt.Errorf("failed starting health server on %s: %w", *addr, err)
	}
	fmt.Printf("=== Praetor Cloud-Native Container Sentinel Running ===\n")
	fmt.Printf("HTTP Probes listening on %s (/healthz, /livez, /readyz)\n", *addr)

	if *auditInterval > 0 {
		startPeriodicAudit(ctx, *auditInterval, *auditPath, hs)
	}

	if err := container.WaitForGracefulDrain(ctx, hs, *drain); err != nil {
		return fmt.Errorf("server drain error: %w", err)
	}
	fmt.Printf("Praetor sentinel stopped gracefully.\n")
	return nil
}

func startPeriodicAudit(ctx context.Context, interval time.Duration, auditPath string, hs *container.HealthServer) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for tick := 0; tick < maxAuditTicks; tick++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			runAuditCheck(ctx, auditPath, hs)
		}
	}()
}

// runAuditCheck flips readiness from the periodic audit result. A scanner failure and a
// real HISS infraction both make the container unready, but they are distinct operational
// conditions, so each is reported on stderr instead of turning /readyz into a silent 503.
func runAuditCheck(ctx context.Context, auditPath string, hs *container.HealthServer) {
	opts := hiss.ScanOptions{Cap: 500, MaxFuncLOC: 60}
	rep, err := hiss.Scan(ctx, auditPath, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] periodic HISS scan of %s failed: %v\n", auditPath, err)
		hs.SetReady(false)
		return
	}
	if rep.TotalInfractions > 0 {
		fmt.Fprintf(os.Stderr, "[WARN] periodic HISS scan of %s reported %d infraction(s); marking unready\n",
			auditPath, rep.TotalInfractions)
		hs.SetReady(false)
		return
	}
	hs.SetReady(true)
}
