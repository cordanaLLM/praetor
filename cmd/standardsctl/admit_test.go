// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/util"
)

const admitHoldLeaseEnv = "ADMIT_TEST_HOLD_EXECUTOR_LEASE"

// needShell skips a test whose step is a POSIX shell command where no sh is on PATH (a Windows
// runner without Git's usr/bin); the admission logic itself is covered by internal/admission.
func needShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH on %s: %v", runtime.GOOS, err)
	}
}

// Positive: the flags set the policy, the limits and the bounds; the label defaults to the
// command's name.
func TestParseAdmit_Positive_FlagsAndDefaults(t *testing.T) {
	opts, err := parseAdmit([]string{"--memory-high", "8G", "--memory-max", "12G", "--memory-swap-max", "0", "--min-available-gib", "20",
		"--wait", "1m", "--", "make", "verify-all"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.label != "make" || opts.policy.MinAvailableBytes != 20<<30 || opts.limits.MemorySwapMax != "0" || opts.wait != time.Minute ||
		strings.Join(opts.argv, " ") != "make verify-all" {
		t.Fatalf("options %+v", opts)
	}
	// Past `--` a word that looks like a flag of admit belongs to the command.
	opts, err = parseAdmit([]string{"--label", "build", "--", "make", "--wait", "-j4"})
	if err != nil || opts.wait != 6*time.Hour || strings.Join(opts.argv, " ") != "make --wait -j4" {
		t.Fatalf("command flags after --: %+v, %v", opts, err)
	}
}

// Negative: no command, a non-positive bound, a threshold past the bound and a limit systemd
// would misread are each refused before anything runs.
func TestParseAdmit_Negative_Refused(t *testing.T) {
	for _, args := range [][]string{
		{"--label", "x"},
		{"--wait", "0s", "--", "true"},
		{"--min-available-gib", "99999999", "--", "true"},
		{"--memory-max", "1.5G", "--", "true"},
		{"--memory-swap-max", "lots", "--", "true"},
	} {
		if _, err := parseAdmit(args); err == nil {
			t.Errorf("parseAdmit(%q) accepted", args)
		}
	}
}

// Negative (planted): a heavy step asked to start under a threshold the host cannot meet is
// held and then refused, and the command never runs. Where the host has no memory source
// (internal/sentinel memory_other.go, macOS among them) admission admits unjudged by design, so
// there the step runs; the branch follows the same reading admission takes, not the system name.
func TestRunAdmit_Negative_UnderThresholdRefused(t *testing.T) {
	needShell(t)
	admitStubTools(t)
	stats, readErr := admission.HostReader(".")()
	out, err := captureStdout(t, func() error {
		return runAdmit([]string{"--local", "--min-available-gib", "1000000", "--wait", "1s", "--poll", "200ms", "--", "sh", "-c", "echo step-ran"})
	})
	if readErr == nil && stats.RAMTotalBytes == 0 {
		if err != nil || !strings.Contains(out, "step-ran") {
			t.Fatalf("on %s memory is not measurable and the step is admitted unjudged: %v, output %q", runtime.GOOS, err, out)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "still held") || strings.Contains(out, "step-ran") {
		t.Fatalf("an under-threshold step was not refused: %v, output %q", err, out)
	}
}

// Boundary: the step's own exit status comes back as praetorctl's, and a passing step returns nil.
func TestRunAdmit_Boundary_ExitStatusPassesThrough(t *testing.T) {
	needShell(t)
	admitStubTools(t)
	err := runAdmit([]string{"--local", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--wait", "10s", "--",
		"sh", "-c", "exit 3"})
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("exit 3 came back as %v", err)
	}
	if err := runAdmit([]string{"--local", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "exit 0"}); err != nil {
		t.Fatalf("a passing step: %v", err)
	}
}

// TestRunAdmit_RemoteHeadroomAndGateStatus proves that remote admission selects the executor with the greatest headroom and preserves child exit codes.
func TestRunAdmit_RemoteHeadroomAndGateStatus(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "adequate", "best", "busy", "down")
	for _, status := range []int{0, 3, 255} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			out, log, code := runAdmitCaptured(t, "--label", "gate-fixture", "--", "sh", "-ec", `
test "$(git rev-parse HEAD)" = "$1"
test "$(git write-tree)" = "$2"
test "$(cat file)" = staged
test ! -e untracked
test "$4" = "argument with 'quotes' and \$literal"
printf 'planted gate stdout %s\n' "$ADMIT_STUB_HOST"
printf 'planted gate stderr\n' >&2
exit "$3"`, "gate", f.head, f.tree, fmt.Sprint(status), "argument with 'quotes' and $literal")
			if code != status || !strings.Contains(out, "planted gate stdout best") {
				t.Fatalf("wanted best executor with exit %d, got %d, stdout %q, stderr %q", status, code, out, log)
			}
			for _, want := range []string{"admitted gate-fixture on best", "busy held: load", "down skipped: probe failed", "planted gate stderr", "GOMAXPROCS="} {
				if !strings.Contains(log, want) {
					t.Errorf("stderr lacks %q: %q", want, log)
				}
			}
			f.assertClean(t)
		})
	}
}

// TestRunAdmit_RemoteTieUsesLoadPerCore proves that tie-breaking between executors with equal headroom chooses the executor with lower load per core.
func TestRunAdmit_RemoteTieUsesLoadPerCore(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "tie-small", "tie-large")
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", `printf '%s' "$ADMIT_STUB_HOST"`)
	if code != 0 || out != "tie-large" || !strings.Contains(log, "on tie-large") {
		t.Fatalf("tie result: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// TestRunAdmit_RemoteRefusesDownAndHeldByName proves that unresponsive or overloaded executors are refused with status 75 when wait timeout expires.
func TestRunAdmit_RemoteRefusesDownAndHeldByName(t *testing.T) {
	for _, name := range []string{"down", "busy", "incomplete", "slow"} {
		t.Run(name, func(t *testing.T) {
			f := newAdmitFixture(t)
			f.inventory(t, name)
			out, log, code := runAdmitCaptured(t, "--wait", "150ms", "--", "sh", "-c", "echo ran")
			if code != 75 || out != "" || !strings.Contains(log, name) || !strings.Contains(log, "no remote executor admitted") {
				t.Fatalf("refusal for %s: %d %q %q", name, code, out, log)
			}
			f.assertClean(t)
		})
	}
}

// TestRunAdmit_RemoteSlowExecutorDoesNotBlockHealthy proves that slow executor probes run concurrently and do not delay admission on healthy executors.
func TestRunAdmit_RemoteSlowExecutorDoesNotBlockHealthy(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "slow", "best")
	start := time.Now()
	out, log, code := runAdmitCaptured(t, "--wait", "8s", "--", "sh", "-c", `printf '%s' "$ADMIT_STUB_HOST"`)
	if code != 0 || out != "best" || !strings.Contains(log, "slow skipped: probe failed") || time.Since(start) >= 6*time.Second {
		t.Fatalf("slow executor blocked a healthy peer: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// TestRunAdmit_RemoteTimeoutCleansWorkspace proves that steps exceeding the admission timeout are killed with status 124 and their workspace is removed.
func TestRunAdmit_RemoteTimeoutCleansWorkspace(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "best")
	out, log, code := runAdmitCaptured(t, "--timeout", "150ms", "--", "sh", "-c", "echo started; sleep 15")
	if code != 124 || out != "started\n" {
		t.Fatalf("timeout: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// TestRunAdmit_RemoteBuildParallelism proves that build parallelism dynamically scales with concurrent executor leases and stale locks are pruned.
func TestRunAdmit_RemoteBuildParallelism(t *testing.T) {
	if runAdmitSubprocessLeaseHolder(t) {
		return
	}
	f := newAdmitFixture(t)
	f.inventory(t, "home")
	leaseDir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "praetor-admit-leases", "home")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(leaseDir, "999999-stale.lock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", `printf "%s %s %s\n" "$(nproc)" "$MAKEFLAGS" "$GOMAXPROCS"`)
	if code != 0 || out != "30 -j12 12\n" || !strings.Contains(log, "cores 12 from list and pod cpu.max; 1 concurrent step(s); -j12 GOMAXPROCS=12") {
		t.Fatalf("one step parallelism: %d %q %q", code, out, log)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale lock file was not removed: %v", err)
	}
	assertNoAdmitLeaseFiles(t, leaseDir)
	stopOther := startAdmitSubprocessLeaseHolder(t, "home")
	out, log, code = runAdmitCaptured(t, "--", "sh", "-c", `printf "%s %s %s\n" "$(nproc)" "$MAKEFLAGS" "$GOMAXPROCS"`)
	if code != 0 || out != "30 -j6 6\n" || !strings.Contains(log, "cores 12 from list and pod cpu.max; 2 concurrent step(s); -j6 GOMAXPROCS=6") {
		t.Fatalf("two concurrent parallelism across processes: %d %q %q", code, out, log)
	}
	stopOther()
	assertNoAdmitLeaseFiles(t, leaseDir)
	f.assertClean(t)
}

// TestRunAdmit_RemoteUnreadableLeaseDirectoryRefused proves that an unreadable lease directory fails closed with an informative error.
func TestRunAdmit_RemoteUnreadableLeaseDirectoryRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unreadable directory permission check requires non-root user")
	}
	f := newAdmitFixture(t)
	f.inventory(t, "home")
	leaseDir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "praetor-admit-leases", "home")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaseDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(leaseDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
	})
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", "echo ran")
	if code != 1 || out != "" || !strings.Contains(log, "executor home: lease directory") {
		t.Fatalf("unreadable lease directory: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// TestRunAdmit_RemoteLeaseCapRefused proves that exceeding the executor lease scan cap refuses admission by name without running the step.
func TestRunAdmit_RemoteLeaseCapRefused(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "home")
	leaseDir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "praetor-admit-leases", "home")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 256 {
		if err := os.WriteFile(filepath.Join(leaseDir, fmt.Sprintf("%d-stale.lock", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", "echo ran")
	if code != 1 || out != "" || strings.Contains(log, "admitted ") ||
		!strings.Contains(log, "executor home: lease directory exceeds lock file limit") {
		t.Fatalf("lease cap refusal: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// assertNoAdmitLeaseFiles asserts that no lingering lease files exist in the specified directory.
func assertNoAdmitLeaseFiles(t *testing.T, leaseDir string) {
	t.Helper()
	entries, err := os.ReadDir(leaseDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("lease files remained after step end: %v %v", entries, err)
	}
}

// runAdmitSubprocessLeaseHolder acquires and holds an executor lease in a subprocess until standard input closes.
func runAdmitSubprocessLeaseHolder(t *testing.T) bool {
	t.Helper()
	name := os.Getenv(admitHoldLeaseEnv)
	if name == "" {
		return false
	}
	_, release, err := admission.AdmitExecutorLease(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	return true
}

// startAdmitSubprocessLeaseHolder spawns a subprocess holding an executor lease and returns a cleanup function to release it.
func startAdmitSubprocessLeaseHolder(t *testing.T, executor string) func() {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	readyR, readyW := io.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		runErr := util.RunCommandPassthrough(ctx, "", "env", stdinR, readyW, &stderr,
			admitHoldLeaseEnv+"="+executor, self, "-test.run=^TestRunAdmit_RemoteBuildParallelism$")
		if closeErr := readyW.CloseWithError(runErr); closeErr != nil {
			runErr = errors.Join(runErr, closeErr)
		}
		done <- runErr
	}()
	var ack [1]byte
	if _, err := io.ReadFull(readyR, ack[:]); err != nil {
		cancel()
		t.Fatalf("subprocess lease holder failed to start: %v (%s)", errors.Join(err, stdinW.Close(), stdinR.Close()), stderr.String())
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			defer cancel()
			closeErr := stdinW.Close()
			_, drainErr := io.Copy(io.Discard, readyR)
			if err := errors.Join(closeErr, drainErr, <-done, stdinR.Close(), readyR.Close()); err != nil {
				t.Fatalf("subprocess lease holder: %v (%s)", err, stderr.String())
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// TestRunAdmit_RemoteNoCoresRefused proves that executors lacking both configured core counts and cgroup quotas are refused.
func TestRunAdmit_RemoteNoCoresRefused(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventoryRaw(t, `{"name":"no-cores","ssh_host":"no-cores","ssh_user":"fixture","workspace_root":"`+f.root+`","memory_gib":64,"class":"cpu-offload"}`)
	out, log, code := runAdmitCaptured(t, "--wait", "150ms", "--", "sh", "-c", "echo ran")
	if code != 75 || out != "" || !strings.Contains(log, "executor no-cores: no core count (list cores missing, cpu.max unlimited)") {
		t.Fatalf("no cores: %d %q %q", code, out, log)
	}
}

// TestRunAdmit_RemoteBadMemoryRefused proves that remote executors with missing, unreadable, or mismatched cgroup memory limits are refused.
func TestRunAdmit_RemoteBadMemoryRefused(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"max-mem", "executor max-mem: pod memory.max max is not the step MemoryMax 25769803776"},
		{"bad-mem", "executor bad-mem: pod memory.max 99999999 is not the step MemoryMax 25769803776"},
		{"large-mem", "executor large-mem: pod memory.max 34359738368 is not the step MemoryMax 25769803776"},
		{"missing-mem", "executor missing-mem: pod memory.max unreadable is not the step MemoryMax 25769803776"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			f := newAdmitFixture(t)
			f.inventory(t, tc.host)
			out, log, code := runAdmitCaptured(t, "--poll", "10ms", "--", "sh", "-c", "echo ran")
			if code != 1 || out != "" || !strings.Contains(log, tc.want) {
				t.Fatalf("bad memory (%s): %d %q %q", tc.host, code, out, log)
			}
			f.assertClean(t)
		})
	}
}

// TestRunAdmit_RemoteEvictionAndFallback proves that unexpected remote disconnections and marker-less
// wrapper terminations fail closed unless local fallback is enabled, while completed 143 steps keep 143.
func TestRunAdmit_RemoteEvictionAndFallback(t *testing.T) {
	needShell(t)
	for _, host := range []string{"evict", "term-evict"} {
		t.Run(host, func(t *testing.T) {
			assertEvictionAndLocalFallback(t, host)
		})
	}
	f := newAdmitFixture(t)
	f.inventory(t, "best")
	out, log, code := runAdmitCaptured(t, "--fallback", "local", "--", "sh", "-c", "exit 143")
	if code != 143 || out != "" || strings.Contains(log, "lost the step mid-run") || strings.Contains(log, "admission fallback") {
		t.Fatalf("completed step 143: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// assertEvictionAndLocalFallback verifies that host fails as lost without --fallback and reruns locally with --fallback local.
func assertEvictionAndLocalFallback(t *testing.T, host string) {
	t.Helper()
	f := newAdmitFixture(t)
	f.inventory(t, host)
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", "echo ran")
	if code == 0 || !strings.Contains(log, "executor "+host+" lost the step mid-run (evicted or unreachable)") || strings.Contains(out, "ran") {
		t.Fatalf("eviction (%s) without fallback: %d %q %q", host, code, out, log)
	}
	out, log, code = runAdmitCaptured(t, "--fallback", "local", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "echo planted-local")
	if code != 0 || out != "planted-local\n" || !strings.Contains(log, "admission fallback: running on local after executor "+host+" lost the step mid-run") {
		t.Fatalf("eviction (%s) with fallback: %d %q %q", host, code, out, log)
	}
}

// TestRunAdmit_RemoteHeadroomUsesPodCgroup proves that available memory is capped by pod cgroup limits and unreadable cgroups are skipped.
func TestRunAdmit_RemoteHeadroomUsesPodCgroup(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "headroom", "unreadable-cgroup")
	out, log, code := runAdmitCaptured(t, "--wait", "150ms", "--", "sh", "-c", "echo ran")
	if code != 75 || out != "" || !strings.Contains(log, "headroom held: available 6.0 GiB < 12.0 GiB") ||
		!strings.Contains(log, "unreadable-cgroup skipped: probe failed: \"executor unreadable-cgroup: pod cgroup counters unreadable\"") {
		t.Fatalf("pod headroom hold: %d %q %q", code, out, log)
	}
	_ = f
}

// TestRunAdmit_RemoteCancellationCleansWorkspace proves that cancelling root context aborts the remote execution and removes the remote workspace.
func TestRunAdmit_RemoteCancellationCleansWorkspace(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "best")
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	rootCtx = ctx
	marker := filepath.Join(t.TempDir(), "started")
	started := make(chan bool, 1)
	go func() {
		for range 500 {
			if _, err := os.Stat(marker); err == nil {
				started <- true
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		started <- false
		cancel()
	}()
	start := time.Now()
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", `: > "$1"; exec sleep 15`, "step", marker)
	if !<-started || code != 1 || !strings.Contains(log, "executor best: SSH run") || time.Since(start) >= 8*time.Second {
		t.Fatalf("cancellation: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

// TestRunAdmit_ExecutorModeIsExplicit proves that remote execution requires an explicit inventory path or local flag, refusing unset configurations.
func TestRunAdmit_ExecutorModeIsExplicit(t *testing.T) {
	admitStubTools(t)
	t.Setenv(admitExecutorEnv, "")
	t.Setenv(admitExecutorsEnv, "")
	step := []string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "echo planted-local"}
	out, log, code := runAdmitCaptured(t, step...)
	if code != 1 || out != "" || !strings.Contains(log, admitExecutorsEnv) {
		t.Fatalf("missing list: %d %q %q", code, out, log)
	}
	missing := filepath.Join(t.TempDir(), "unreadable.json")
	t.Setenv(admitExecutorsEnv, missing)
	out, log, code = runAdmitCaptured(t, step...)
	if code != 1 || out != "" || !strings.Contains(log, missing) {
		t.Fatalf("unreadable list: %d %q %q", code, out, log)
	}
	assertNamedLocal(t, append([]string{"--local"}, step...))
	t.Setenv(admitExecutorEnv, "local")
	assertNamedLocal(t, step)
}

// assertNamedLocal asserts that running admit with the given arguments executes locally with named status output.
func assertNamedLocal(t *testing.T, args []string) {
	t.Helper()
	out, log, code := runAdmitCaptured(t, args...)
	if code != 0 || out != "planted-local\n" || !strings.Contains(log, "on local (named)") {
		t.Fatalf("named local: %d %q %q", code, out, log)
	}
}

// runAdmitCaptured dispatches the admit command with the provided arguments and captures stdout, stderr, and the resulting exit code.
func runAdmitCaptured(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var captureErr error
	stderr, captureErr = captureStderr(t, func() error {
		var runErr error
		stdout, runErr = captureStdout(t, func() error {
			return dispatchCommandFrom(t.Context(), "admit", args)
		})
		if runErr != nil {
			code = commandExitCode(os.Stderr, runErr)
		}
		return nil
	})
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	return stdout, stderr, code
}

type admitFixture struct {
	root, head, tree string
}

// newAdmitFixture sets up a temporary Git repository and mock environment for testing remote admission workflows.
func newAdmitFixture(t *testing.T) admitFixture {
	t.Helper()
	admitStubTools(t)
	for _, tool := range []string{"git", "timeout", "head", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("fake executor requires %s: %v", tool, err)
		}
	}
	t.Setenv(admitExecutorEnv, "")
	root := filepath.Join(t.TempDir(), "remote 'workspace'")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	work := filepath.Join(repo, "sub dir")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string { return admitFixtureGit(t, repo, args...) }
	git("init", "-q", "--template=")
	writeFixtureFile(t, work, "file", "committed\n")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false",
		"-c", "maintenance.auto=false", "commit", "-qm", "fixture")
	head := git("rev-parse", "HEAD")
	writeFixtureFile(t, work, "file", "staged\n")
	git("add", ".")
	tree := git("write-tree")
	writeFixtureFile(t, work, "file", "unstaged\n")
	writeFixtureFile(t, work, "untracked", "untracked\n")
	t.Chdir(work)
	return admitFixture{root: root, head: head, tree: tree}
}

// admitFixtureGit executes a Git command within the fixture directory using an isolated environment.
func admitFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, admitFixtureEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatalf("fixture git %v: %v", args, err)
	}
	return out
}

// admitFixtureEnvironment returns an isolated environment with host Git variables stripped and global Git configs redirected.
func admitFixtureEnvironment() []string {
	env := util.FilterEnvironment(os.Environ(), func(name string) bool {
		return strings.HasPrefix(strings.ToUpper(name), "GIT_")
	})
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

// admitStubTools installs mock executables for SSH, systemd, and system tools into the test environment's PATH.
func admitStubTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake SSH and systemd-run require POSIX execution")
	}
	bin := t.TempDir()
	writeAdmitExecutable(t, bin, "ssh", admitFakeSSH)
	writeAdmitExecutable(t, bin, "systemd-run", admitFakeScope)
	writeAdmitExecutable(t, bin, "systemctl", admitFakeSystemctl)
	writeAdmitExecutable(t, bin, "nproc", "#!/bin/sh\necho 30\n")
	t.Setenv("ADMIT_STUB_SCOPES", t.TempDir())
	t.Setenv("ADMIT_STUB_CGROUPS", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, err := util.WithCommandEnvironment(t.Context(), admitFixtureEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	previous := rootCtx
	t.Cleanup(func() { rootCtx = previous })
	rootCtx = ctx
}

// writeAdmitExecutable writes an executable script into the specified directory with 0700 permissions.
func writeAdmitExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	file := writeFixtureFile(t, dir, name, body)
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a stub run through PATH needs the owner execute bit
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatal(err)
	}
}

// inventory generates an executors.json file containing standard definitions for the specified executor names.
func (f admitFixture) inventory(t *testing.T, names ...string) {
	t.Helper()
	var entries []string
	for _, name := range names {
		cores := 8
		switch name {
		case "tie-large":
			cores = 16
		case "home":
			cores = 30
		}
		entries = append(entries, fmt.Sprintf(`{"name":%q,"ssh_host":%q,"ssh_user":"fixture","workspace_root":%q,"cores":%d,"memory_gib":64,"class":"cpu-offload"}`,
			name, name, f.root, cores))
	}
	file := writeFixtureFile(t, t.TempDir(), "executors.json", "["+strings.Join(entries, ",")+"]")
	t.Setenv(admitExecutorsEnv, file)
}

// inventoryRaw writes verbatim executor JSON entries into the fixture's executors.json configuration file.
func (f admitFixture) inventoryRaw(t *testing.T, entries ...string) {
	t.Helper()
	file := writeFixtureFile(t, t.TempDir(), "executors.json", "["+strings.Join(entries, ",")+"]")
	t.Setenv(admitExecutorsEnv, file)
}

// assertClean asserts that all workspace directories created during remote execution were cleaned up.
func (f admitFixture) assertClean(t *testing.T) {
	t.Helper()
	if files, err := os.ReadDir(f.root); err != nil || len(files) != 0 {
		t.Fatalf("workspace was not cleaned: %v %v", files, err)
	}
}

const admitMemoryMaxBytes = "25769803776"

const admitFakeSSH = `#!/bin/sh
set -eu
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24; do
    if [ "$1" = -- ]; then shift; break; fi
    shift
done
host=$1 command=$2
cgroup="$ADMIT_STUB_CGROUPS/$host"
mkdir -p "$cgroup"
export PRAETOR_ADMIT_CGROUP_DIR="$cgroup"
write_cgroups() {
    memmax=$1; memcur=$2; cpumax=$3
    if [ "$memmax" = __UNREADABLE__ ]; then
        rm -f "$cgroup/memory.max"
    else
        printf '%s\n' "$memmax" > "$cgroup/memory.max"
    fi
    printf '%s\n' "$memcur" > "$cgroup/memory.current"
    printf '%s\n' "$cpumax" > "$cgroup/cpu.max"
}
stub_probe() {
    load=$1; available=$2; memmax=$3; memcur=$4; cpumax=$5
    write_cgroups "$memmax" "$memcur" "$cpumax"
    printf '%s 0 0 1/10 123\n' "$load"
    printf '__PRAETOR_CGROUP_END__\n'
    printf 'MemTotal: 67108864 kB\nMemAvailable: %s kB\nSwapTotal: 33554432 kB\nSwapFree: 33554432 kB\n' "$available"
    printf '__PRAETOR_CGROUP_END__\n'
    for f in "$cgroup/memory.max" "$cgroup/memory.current" "$cgroup/cpu.max"; do
        cat "$f" 2>/dev/null || echo __MISSING__
        printf '__PRAETOR_CGROUP_END__\n'
    done
}
case "$command" in
    *'for f in /proc/loadavg'*)
        load=1 available=16777216 memmax=` + admitMemoryMaxBytes + ` memcur=0 cpumax="max 100000"
        case "$host" in
            adequate) ;;
            best) available=33554432; cpumax="1200000 100000" ;;
            busy) available=50331648; load=11; cpumax="1200000 100000" ;;
            tie-small) load=2 ;;
            tie-large) load=3 ;;
            home) cpumax="1200000 100000" ;;
            no-cores) cpumax="max 100000" ;;
            down) echo 'fixture connection refused' >&2; exit 255 ;;
            incomplete) printf '1 0 0\n__PRAETOR_CGROUP_END__\n'; exit 0 ;;
            slow) exec sleep 15 ;;
            headroom) available=20971520; memmax=17179869184; memcur=10737418240 ;;
            unreadable-cgroup) memmax=__UNREADABLE__ ;;
            evict|term-evict|bad-mem|max-mem|large-mem|missing-mem) cpumax="1200000 100000" ;;
            *) exit 99 ;;
        esac
        stub_probe "$load" "$available" "$memmax" "$memcur" "$cpumax"
        ;;
    *)
        memmax=` + admitMemoryMaxBytes + `
        case "$host" in
            max-mem) memmax=max ;;
            bad-mem) memmax=99999999 ;;
            large-mem) memmax=34359738368 ;;
            missing-mem) memmax=__UNREADABLE__ ;;
            evict) echo 'fixture mid-run disconnect' >&2; exit 255 ;;
            term-evict) echo 'fixture wrapper sigterm' >&2; exit 143 ;;
        esac
        write_cgroups "$memmax" 0 "1200000 100000"
        export ADMIT_STUB_HOST="$host"
        exec sh -c "exec $command"
        ;;
esac
`

const admitFakeScope = `#!/bin/sh
set -eu
high= max= swap= unit=
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16; do
    case "$1" in
        --property=MemoryHigh=16G) high=yes ;;
        --property=MemoryMax=24G) max=yes ;;
        --property=MemorySwapMax=4G) swap=yes ;;
        --unit=*) unit=${1#--unit=} ;;
        --) shift; break ;;
        true) exit 0 ;;
    esac
    shift
done
test "$high$max$swap" = yesyesyes
printf '%s\n' "$$" > "$ADMIT_STUB_SCOPES/$unit.scope"
echo 'planted scope limits' >&2
exec "$@"
`

const admitFakeSystemctl = `#!/bin/sh
set -eu
test "$1" = --user && test "$2" = stop
pid=$(cat "$ADMIT_STUB_SCOPES/$3")
kill -KILL "-$pid" 2>/dev/null || :
echo 'planted scope stopped' >&2
`
