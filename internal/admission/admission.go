// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package admission holds a heavy step (a build, a test suite, a gate, a landing, a local-model
// job, a background session start) until the host can take it, then runs it in its own memory-
// limited systemd scope, so the out-of-memory daemon reclaims from the step instead of from the
// editor's scope every agent session runs in (#1131). Memory, swap and load are read through
// internal/sentinel; the policy refuses on a failed reading, and a platform that offers no
// reading is admitted with that said, never silently.
package admission

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

const gib = 1 << 30

// Policy is what a host must show before a heavy step starts.
type Policy struct {
	// MinAvailableBytes is the least MemAvailable the host must report.
	MinAvailableBytes uint64
	// MinSwapFreeBytes is the least SwapFree the host must report, where swap is measured.
	MinSwapFreeBytes uint64
	// MaxLoad1 is the highest one-minute load average admitted, where the load is measured.
	MaxLoad1 float64
}

// DefaultPolicy is the fleet's memory rule (12 GiB available, 15 GiB swap free) and a load bound
// of 1.25 times the host's CPUs, the 40 on 32 cores the landing queue admits at.
func DefaultPolicy() Policy {
	return Policy{MinAvailableBytes: 12 * gib, MinSwapFreeBytes: 15 * gib, MaxLoad1: 1.25 * float64(runtime.NumCPU())}
}

// HostCappedPolicy is the fleet's admission policy capped by the host it reads: at most a quarter of
// the host's memory must be available and at most half of its swap free. The fleet bounds (12
// GiB, 15 GiB) hold on a host large enough for them; on a smaller host or a CI runner with a few
// GiB of swap a fixed bound would hold the stage until its deadline on every run.
func HostCappedPolicy(stats sentinel.HostStats) Policy {
	policy := DefaultPolicy()
	if stats.RAMTotalBytes > 0 {
		policy.MinAvailableBytes = min(policy.MinAvailableBytes, stats.RAMTotalBytes/4)
	}
	if stats.SwapMeasured {
		policy.MinSwapFreeBytes = min(policy.MinSwapFreeBytes, stats.SwapTotalBytes/2)
	}
	return policy
}

// Verdict is one admission decision and the reason it names.
type Verdict struct {
	Admit  bool
	Reason string
}

// Judge decides on one reading. A reading with no memory figure (a platform without a source)
// admits and says so; every measured figure below its bound holds the step.
func Judge(stats sentinel.HostStats, p Policy) Verdict {
	if stats.RAMTotalBytes == 0 {
		return Verdict{Admit: true, Reason: "memory is not measurable on " + runtime.GOOS + "; admitted unjudged"}
	}
	if stats.RAMFreeBytes < p.MinAvailableBytes {
		return Verdict{Reason: fmt.Sprintf("available %.1f GiB < %.1f GiB", gibs(stats.RAMFreeBytes), gibs(p.MinAvailableBytes))}
	}
	if stats.SwapMeasured && stats.SwapFreeBytes < p.MinSwapFreeBytes {
		return Verdict{Reason: fmt.Sprintf("swap free %.1f GiB < %.1f GiB", gibs(stats.SwapFreeBytes), gibs(p.MinSwapFreeBytes))}
	}
	if stats.CPULoadMeasured && p.MaxLoad1 > 0 && stats.CPULoad1Min > p.MaxLoad1 {
		return Verdict{Reason: fmt.Sprintf("load %.1f > %.1f", stats.CPULoad1Min, p.MaxLoad1)}
	}
	return Verdict{Admit: true, Reason: fmt.Sprintf("available %.1f GiB, swap free %s, load %s",
		gibs(stats.RAMFreeBytes), swapText(stats), loadText(stats))}
}

func gibs(b uint64) float64 { return float64(b) / gib }

// ReadingsText formats the available memory and free swap readings of stats
// (order engine-hook-build-scope-gate-20261027).
func ReadingsText(stats sentinel.HostStats) string {
	return fmt.Sprintf("available %.1f GiB, swap free %s", gibs(stats.RAMFreeBytes), swapText(stats))
}

func swapText(stats sentinel.HostStats) string {
	if !stats.SwapMeasured {
		return "unmeasured"
	}
	return fmt.Sprintf("%.1f GiB", gibs(stats.SwapFreeBytes))
}

func loadText(stats sentinel.HostStats) string {
	if !stats.CPULoadMeasured {
		return "unmeasured"
	}
	return fmt.Sprintf("%.1f", stats.CPULoad1Min)
}

// Waiter holds a step until the host admits it. Read and Sleep are the host; tests plant their
// own. Log receives one line per held reading and must be set.
type Waiter struct {
	Policy Policy
	// MaxWait bounds the wait (HISS-02); a step still held then is refused.
	MaxWait time.Duration
	// Poll is the time between readings.
	Poll  time.Duration
	Read  func() (sentinel.HostStats, error)
	Sleep func(context.Context, time.Duration) error
	Log   io.Writer
}

// maxPolls bounds the wait loop however small Poll is (HISS-02).
const maxPolls = 100000

// Wait reads the host until Judge admits, writing one line per held reading. A failed reading
// refuses at once (fail closed); a wait past MaxWait refuses with the last reason.
func (w Waiter) Wait(ctx context.Context, label string) (Verdict, error) {
	deadline := time.Now().Add(w.MaxWait)
	for i := 0; i < maxPolls; i++ {
		stats, err := w.Read()
		if err != nil {
			return Verdict{}, fmt.Errorf("admission %s: host reading failed, refused: %w", label, err)
		}
		verdict := Judge(stats, w.Policy)
		if verdict.Admit {
			return verdict, nil
		}
		if !time.Now().Add(w.Poll).Before(deadline) {
			return verdict, fmt.Errorf("admission %s: still held after %s (%s), refused", label, w.MaxWait, verdict.Reason)
		}
		if _, err := fmt.Fprintf(w.Log, "%s admission wait %s: %s\n", time.Now().Format("15:04:05"), label, verdict.Reason); err != nil {
			return verdict, err
		}
		if err := w.Sleep(ctx, w.Poll); err != nil {
			return verdict, fmt.Errorf("admission %s: %w", label, err)
		}
	}
	return Verdict{}, fmt.Errorf("admission %s: more than %d readings, refused", label, maxPolls)
}

// HostReader reads the live host through internal/sentinel; path names the disk sentinel reads.
func HostReader(path string) func() (sentinel.HostStats, error) {
	return func() (sentinel.HostStats, error) { return sentinel.ReadHostStats(path) }
}

// SleepContext sleeps for d or until ctx ends.
func SleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
