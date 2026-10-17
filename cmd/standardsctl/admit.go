// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
)

// admitUsage is the usage line `admit` prints on a malformed call.
const admitUsage = "usage: praetorctl admit [--label L] [--memory-high 16G] [--memory-max 24G] [--memory-swap-max 4G] [--min-available-gib 12] " +
	"[--min-swap-free-gib 15] [--max-load N] [--wait 6h] [--timeout 6h] -- <command> [args...]"

// maxAdmitGiB bounds the memory thresholds a call may ask for, far above any workstation.
const maxAdmitGiB = 1 << 20

// admitOptions are the flags of one `admit` call.
type admitOptions struct {
	label      string
	policy     admission.Policy
	limits     admission.Limits
	wait, poll time.Duration
	timeout    time.Duration
	argv       []string
}

// runAdmit is `praetorctl admit`: it holds a heavy step until the host has the memory, swap and
// load the fleet's rule asks for (internal/admission), then runs it in its own memory-limited
// systemd scope, and exits with the step's own status (#1131). Run it in the background: it
// returns when the step ends, and prints one line when it is admitted.
func runAdmit(args []string) error {
	opts, err := parseAdmit(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(opts.wait + opts.timeout)
	defer cancel()
	waiter := admission.Waiter{Policy: opts.policy, MaxWait: opts.wait, Poll: opts.poll,
		Read: admission.HostReader("."), Sleep: admission.SleepContext, Log: os.Stderr}
	verdict, err := waiter.Wait(ctx, opts.label)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(os.Stderr, "%s admitted %s: %s\n", time.Now().Format("15:04:05"), opts.label, verdict.Reason); err != nil {
		return err
	}
	stepCtx, stepCancel := context.WithTimeout(ctx, opts.timeout)
	defer stepCancel()
	return childExitStatus(admission.Run(stepCtx, admission.DefaultStep(opts.label, opts.argv, opts.limits)))
}

// parseAdmit reads the flags and the command after `--`; everything past `--` is the command,
// whatever it looks like.
func parseAdmit(args []string) (admitOptions, error) {
	fs := flag.NewFlagSet("admit", flag.ContinueOnError)
	label := fs.String("label", "", "name of the step in admission lines and its scope (default: the command's name)")
	high := fs.String("memory-high", "16G", "MemoryHigh of the step's scope: above it the kernel reclaims from the step")
	limit := fs.String("memory-max", "24G", "MemoryMax of the step's scope: above it the kernel swaps the step out")
	swapMax := fs.String("memory-swap-max", "4G", "MemorySwapMax of the step's scope: past MemoryMax plus this the step alone is killed; 0 keeps it out of swap")
	wait := fs.Duration("wait", 6*time.Hour, "longest the step is held before it is refused")
	poll := fs.Duration("poll", time.Minute, "time between host readings while held")
	timeout := fs.Duration("timeout", 6*time.Hour, "longest the step runs once admitted")
	policy := admission.DefaultPolicy()
	minAvail := fs.Uint64("min-available-gib", policy.MinAvailableBytes>>30, "least MemAvailable, in GiB, the host must report")
	minSwap := fs.Uint64("min-swap-free-gib", policy.MinSwapFreeBytes>>30, "least SwapFree, in GiB, the host must report")
	maxLoad := fs.Float64("max-load", policy.MaxLoad1, "highest one-minute load average admitted (default 1.25 per CPU)")
	argv, err := parseInterspersed(fs, args)
	if err != nil {
		return admitOptions{}, err
	}
	if len(argv) == 0 {
		return admitOptions{}, errors.New(admitUsage)
	}
	if *wait <= 0 || *poll <= 0 || *timeout <= 0 {
		return admitOptions{}, errors.New("--wait, --poll and --timeout must be positive")
	}
	if *minAvail > maxAdmitGiB || *minSwap > maxAdmitGiB || *maxLoad < 0 {
		return admitOptions{}, fmt.Errorf("--min-available-gib and --min-swap-free-gib must be at most %d, --max-load at least 0", maxAdmitGiB)
	}
	policy = admission.Policy{MinAvailableBytes: *minAvail << 30, MinSwapFreeBytes: *minSwap << 30, MaxLoad1: *maxLoad}
	opts := admitOptions{label: *label, policy: policy, limits: admission.Limits{MemoryHigh: *high, MemoryMax: *limit, MemorySwapMax: *swapMax},
		wait: *wait, poll: *poll, timeout: *timeout, argv: argv}
	if opts.label == "" {
		opts.label = argv[0]
	}
	return opts, opts.limits.Check()
}
