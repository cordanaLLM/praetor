// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
)

// admitUsage is the usage line `admit` prints on a malformed call.
const admitUsage = "usage: praetorctl admit [--local] [--fallback MODE] [--label L] [--memory-high 16G] [--memory-max 24G] [--memory-swap-max 4G] [--min-available-gib 12] " +
	"[--min-swap-free-gib 15] [--max-load N] [--wait 6h] [--timeout 6h] -- <command> [args...]"

const admitExecutorEnv = "PRAETOR_ADMIT_EXECUTOR"
const admitExecutorsEnv = "PRAETOR_ADMIT_EXECUTORS"

// maxAdmitGiB bounds the memory thresholds a call may ask for, far above any workstation.
const maxAdmitGiB = 1 << 20

// admitOptions are the flags of one `admit` call.
type admitOptions struct {
	label        string
	policy       admission.Policy
	limits       admission.Limits
	wait, poll   time.Duration
	timeout      time.Duration
	argv         []string
	local        bool
	defaultLoad  bool
	fallbackMode string
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
	mode := os.Getenv(admitExecutorEnv)
	if opts.local || mode == "local" {
		return runLocalAdmit(opts)
	}
	if mode != "" {
		return fmt.Errorf("invalid %s=%q, use local or leave it unset", admitExecutorEnv, mode)
	}
	return runRemoteAdmit(opts, os.Getenv(admitExecutorsEnv))
}

// runLocalAdmit waits for local host admission and runs the step on the workstation.
func runLocalAdmit(opts admitOptions) error {
	ctx, cancel := commandContext(opts.wait)
	defer cancel()
	label := opts.label + " on local (named)"
	waiter := admission.Waiter{Policy: opts.policy, MaxWait: opts.wait, Poll: opts.poll,
		Read: admission.HostReader("."), Sleep: admission.SleepContext, Log: os.Stderr}
	verdict, err := waiter.Wait(ctx, label)
	if err != nil {
		return err
	}
	if err := logAdmitted(label, verdict); err != nil {
		return err
	}
	stepCtx, stepCancel := commandContext(opts.timeout)
	defer stepCancel()
	return childExitStatus(admission.Run(stepCtx, admission.DefaultStep(label, opts.argv, opts.limits)))
}

// runRemoteAdmit loads the remote executor list, waits for an admitted host, and executes the step remotely.
func runRemoteAdmit(opts admitOptions, path string) error {
	if path == "" {
		return fmt.Errorf("%s must name the remote executor list, or use --local", admitExecutorsEnv)
	}
	executors, err := admission.ReadExecutors(path)
	if err != nil {
		return fmt.Errorf("load %s=%q: %w", admitExecutorsEnv, path, err)
	}
	ctx := rootContext()
	waiter := admission.RemoteWaiter{Executors: executors, Policy: opts.policy, MaxWait: opts.wait, Poll: opts.poll,
		DefaultLoad: opts.defaultLoad, Log: os.Stderr}
	executor, info, verdict, err := waiter.Wait(ctx, opts.label)
	if err != nil {
		if errors.Is(err, admission.ErrNoExecutor) && ctx.Err() == nil {
			return admitStatus(75, err)
		}
		return err
	}
	return runAdmittedRemoteStep(opts, executor, info, verdict)
}

// runAdmittedRemoteStep acquires an executor lease, calculates job parallelism, and executes the step on the admitted remote host.
func runAdmittedRemoteStep(opts admitOptions, executor admission.Executor, info admission.RemoteAdmitInfo, verdict admission.Verdict) (resultErr error) {
	concurrent, releaseLease, err := admission.AdmitExecutorLease(executor.Name)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, releaseLease())
	}()
	info.Concurrent = concurrent
	info.Jobs = admission.StepParallelismJobs(info.Cores, concurrent)
	if err := logAdmittedRemote(opts.label, executor.Name, info, verdict); err != nil {
		return err
	}
	memoryMax, err := admission.LimitsMemoryMaxBytes(opts.limits)
	if err != nil {
		return err
	}
	code, err := admission.RunRemote(rootContext(), executor, admission.DefaultStep(opts.label, opts.argv, opts.limits), opts.timeout, info.Jobs, memoryMax)
	if errors.Is(err, admission.ErrExecutorLost) && opts.fallbackMode == "local" {
		if _, werr := fmt.Fprintf(os.Stderr, "%s admission fallback: running on local after executor %s lost the step mid-run\n",
			time.Now().Format("15:04:05"), executor.Name); werr != nil {
			return werr
		}
		return errors.Join(releaseLease(), runLocalAdmit(opts))
	}
	return admitStatus(code, err)
}

// logAdmitted writes the local admission timestamp, label, and verdict reason to standard error.
func logAdmitted(label string, verdict admission.Verdict) error {
	_, err := fmt.Fprintf(os.Stderr, "%s admitted %s: %s\n", time.Now().Format("15:04:05"), label, verdict.Reason)
	return err
}

// logAdmittedRemote writes the remote admission details, core count source, and concurrency parameters to standard error.
func logAdmittedRemote(label, executor string, info admission.RemoteAdmitInfo, verdict admission.Verdict) error {
	line := fmt.Sprintf("%s admitted %s on %s: %s; cores %d from %s; %d concurrent step(s); -j%d GOMAXPROCS=%d",
		time.Now().Format("15:04:05"), label, executor, verdict.Reason, info.Cores, info.CoreSource, info.Concurrent, info.Jobs, info.Jobs)
	_, err := fmt.Fprintln(os.Stderr, line)
	return err
}

// admitStatus converts a non-zero exit code into an exitStatusError, logging and joining any accompanying error.
func admitStatus(code int, err error) error {
	if code == 0 {
		return err
	}
	status := exitStatusError{code: code}
	if err == nil {
		return status
	}
	_, writeErr := fmt.Fprintln(os.Stderr, err)
	return errors.Join(status, err, writeErr)
}

// parseAdmit reads the flags and the command after `--`; everything past `--` is the command,
// whatever it looks like.
func parseAdmit(args []string) (admitOptions, error) {
	fs := flag.NewFlagSet("admit", flag.ContinueOnError)
	label := fs.String("label", "", "name of the step in admission lines and its scope (default: the command's name)")
	local := fs.Bool("local", false, "run on the workstation explicitly; otherwise use "+admitExecutorsEnv)
	fallback := fs.String("fallback", "", "when a remote executor loses a step mid-run, run on the workstation if MODE is local")
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
	policy, err = admitPolicy(*minAvail, *minSwap, *maxLoad)
	if err != nil {
		return admitOptions{}, err
	}
	if *fallback != "" && *fallback != "local" {
		return admitOptions{}, fmt.Errorf("--fallback %q is invalid, use local", *fallback)
	}
	opts := admitOptions{label: *label, policy: policy, limits: admission.Limits{MemoryHigh: *high, MemoryMax: *limit, MemorySwapMax: *swapMax},
		wait: *wait, poll: *poll, timeout: *timeout, argv: argv, local: *local, defaultLoad: true, fallbackMode: *fallback}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-load" {
			opts.defaultLoad = false
		}
	})
	if opts.label == "" {
		opts.label = argv[0]
	}
	return opts, opts.validate()
}

// admitPolicy constructs an admission policy from GiB thresholds and load average, refusing values exceeding maxAdmitGiB or negative loads.
func admitPolicy(minAvail, minSwap uint64, maxLoad float64) (admission.Policy, error) {
	if minAvail > maxAdmitGiB || minSwap > maxAdmitGiB || maxLoad < 0 || math.IsNaN(maxLoad) || math.IsInf(maxLoad, 0) {
		return admission.Policy{}, fmt.Errorf("--min-available-gib and --min-swap-free-gib must be at most %d, --max-load finite and at least 0", maxAdmitGiB)
	}
	return admission.Policy{MinAvailableBytes: minAvail << 30, MinSwapFreeBytes: minSwap << 30, MaxLoad1: maxLoad}, nil
}

// validate checks that durations are positive, timeout does not exceed MaxRemoteStepTimeout, and resource limits are valid.
func (o admitOptions) validate() error {
	if o.wait <= 0 || o.poll <= 0 || o.timeout <= 0 {
		return errors.New("--wait, --poll and --timeout must be positive")
	}
	if o.timeout > admission.MaxRemoteStepTimeout {
		return errors.New("--timeout exceeds the supported duration")
	}
	return o.limits.Check()
}
