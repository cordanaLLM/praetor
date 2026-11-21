// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission/ptytest"
	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const hogEnv = "ADMISSION_TEST_HOG"

// TestMain lets the test binary re-run itself as a memory hog for the planted spike.
func TestMain(m *testing.M) {
	if os.Getenv(hogEnv) == "1" {
		hog := make([]byte, 1<<30)
		for i := 0; i < len(hog); i += 4096 {
			hog[i] = 1
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var policy = Policy{MinAvailableBytes: 12 * gib, MinSwapFreeBytes: 15 * gib, MaxLoad1: 40}

func host(availGiB, swapGiB uint64, load float64) sentinel.HostStats {
	return sentinel.HostStats{RAMTotalBytes: 64 * gib, RAMFreeBytes: availGiB * gib, SwapMeasured: true, SwapTotalBytes: 100 * gib,
		SwapFreeBytes: swapGiB * gib, CPULoadMeasured: true, CPULoad1Min: load}
}

// Positive: a host above every bound admits, naming what it read.
func TestJudge_Positive_AdmitsAboveEveryBound(t *testing.T) {
	stats := host(20, 50, 10)
	v := Judge(stats, policy)
	if !v.Admit || !strings.Contains(v.Reason, "available 20.0 GiB") {
		t.Fatalf("verdict %+v", v)
	}
	if got := ReadingsText(stats); got != "available 20.0 GiB, swap free 50.0 GiB" {
		t.Fatalf("readings text %q", got)
	}
}

// Negative: memory, swap or load past its bound holds the step and names which one.
func TestJudge_Negative_HoldsBelowABound(t *testing.T) {
	for want, stats := range map[string]sentinel.HostStats{
		"available 8.0 GiB < 12.0 GiB": host(8, 50, 10), "swap free 4.0 GiB < 15.0 GiB": host(20, 4, 10), "load 60.0 > 40.0": host(20, 50, 60),
	} {
		if v := Judge(stats, policy); v.Admit || v.Reason != want {
			t.Errorf("verdict %+v, want held for %q", v, want)
		}
		if got := ReadingsText(stats); !strings.Contains(got, "available") || !strings.Contains(got, "swap free") {
			t.Errorf("readings text %q", got)
		}
	}
}

// Boundary: exactly at a bound admits; unmeasured swap and load are not judged; a platform with
// no memory source admits and says so.
func TestJudge_Boundary_EdgesAndUnmeasured(t *testing.T) {
	if !Judge(host(12, 15, 40), policy).Admit {
		t.Fatal("a host exactly at the bounds was held")
	}
	s := host(20, 0, 99)
	s.SwapMeasured, s.CPULoadMeasured = false, false
	if v := Judge(s, policy); !v.Admit || !strings.Contains(v.Reason, "swap free unmeasured") {
		t.Fatalf("unmeasured swap/load verdict %+v", v)
	}
	if got := ReadingsText(s); got != "available 20.0 GiB, swap free unmeasured" {
		t.Fatalf("unmeasured readings text %q", got)
	}
	if v := Judge(sentinel.HostStats{}, policy); !v.Admit || !strings.Contains(v.Reason, "admitted unjudged") {
		t.Fatalf("no memory source verdict %+v", v)
	}
}

func waiter(readings []sentinel.HostStats, readErr error, maxWait time.Duration, log *bytes.Buffer) (Waiter, *int) {
	calls := 0
	return Waiter{Policy: policy, MaxWait: maxWait, Poll: time.Millisecond, Log: log,
		Read: func() (sentinel.HostStats, error) {
			calls++
			if readErr != nil {
				return sentinel.HostStats{}, readErr
			}
			return readings[min(calls-1, len(readings)-1)], nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil }}, &calls
}

// Positive (planted): a step started under the threshold is held, with a line saying why, and
// admitted once the host frees memory.
func TestWait_Positive_HeldThenAdmitted(t *testing.T) {
	var log bytes.Buffer
	w, calls := waiter([]sentinel.HostStats{host(4, 50, 10), host(4, 50, 10), host(20, 50, 10)}, nil, time.Hour, &log)
	v, err := w.Wait(context.Background(), "verify-all")
	if err != nil || !v.Admit || *calls != 3 || strings.Count(log.String(), "admission wait verify-all: available 4.0 GiB") != 2 {
		t.Fatalf("verdict %+v, err %v, %d readings, log %q", v, err, *calls, log.String())
	}
}

// Negative: a host that stays under the threshold refuses the step once MaxWait passes; a failed
// reading refuses at once (fail closed).
func TestWait_Negative_RefusedOnTimeoutAndReadError(t *testing.T) {
	var log bytes.Buffer
	w, _ := waiter([]sentinel.HostStats{host(4, 50, 10)}, nil, 0, &log)
	if _, err := w.Wait(context.Background(), "gate"); err == nil || !strings.Contains(err.Error(), "still held") {
		t.Fatalf("a host under the threshold was not refused: %v", err)
	}
	w, calls := waiter(nil, errors.New("meminfo unreadable"), time.Hour, &log)
	if _, err := w.Wait(context.Background(), "gate"); err == nil || !strings.Contains(err.Error(), "refused") || *calls != 1 {
		t.Fatalf("a failed reading was not refused at once: %v after %d readings", err, *calls)
	}
}

// Boundary: a cancelled context ends the wait with the context's error.
func TestWait_Boundary_CancelledContext(t *testing.T) {
	var log bytes.Buffer
	w, _ := waiter([]sentinel.HostStats{host(4, 50, 10)}, nil, time.Hour, &log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Sleep = SleepContext
	if _, err := w.Wait(ctx, "gate"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
}

// Positive and negative: limits systemd reads as intended pass; fractions, signs and missing
// units fail.
func TestLimitsCheck(t *testing.T) {
	if err := (Limits{MemoryHigh: "16G", MemoryMax: "512M", MemorySwapMax: "0"}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := (Limits{MemoryHigh: "16G", MemoryMax: "24G", MemorySwapMax: "4G"}).Check(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Limits{{"16", "24G", "0"}, {"1.5G", "24G", "0"}, {"16G", "-1G", "0"}, {"", "24G", "0"}, {"16G", "infinity", "0"},
		{"16G", "24G", ""}, {"16G", "24G", "-1"}} {
		if bad.Check() == nil {
			t.Errorf("limits %+v accepted", bad)
		}
	}
}

// Boundary: the scope argv carries both limits and --expand-environment=no, and a label with
// path characters becomes a safe, bounded unit name.
func TestScopeArgsAndUnitName(t *testing.T) {
	args := strings.Join(ScopeArgs("admit-x", Limits{MemoryHigh: "8G", MemoryMax: "12G", MemorySwapMax: "2G"}, []string{"make", "verify-all"}), " ")
	for _, want := range []string{"--user --scope", "--expand-environment=no", "--property=MemoryHigh=8G", "--property=MemoryMax=12G",
		"--property=MemorySwapMax=2G", "-- make verify-all"} {
		if !strings.Contains(args, want) {
			t.Errorf("scope args %q lack %q", args, want)
		}
	}
	name := unitName("go test ./... -race/"+strings.Repeat("x", 80), time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC))
	if strings.ContainsAny(name, " /") || !strings.HasPrefix(name, "admit-go-test-") || len(name) > 80 {
		t.Fatalf("unit name %q", name)
	}
}

func requireScopes(t *testing.T) {
	t.Helper()
	if ok, why := ScopesAvailable(context.Background()); !ok {
		t.Skipf("no user scopes here: %s", why)
	}
}

// Positive (read-back): the step runs inside its scope, which carries the MemoryMax it was given.
func TestRun_Positive_ScopeCarriesTheLimit(t *testing.T) {
	requireScopes(t)
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	step := Step{Label: "readback", Limits: Limits{MemoryHigh: "96M", MemoryMax: "128M", MemorySwapMax: "0"}, Stdout: &out, Stderr: &errOut,
		Argv: []string{"sh", "-c", `cat "/sys/fs/cgroup$(cut -d: -f3 /proc/self/cgroup)/memory.max"`}}
	if err := Run(ctx, step); err != nil {
		t.Fatalf("run: %v (%s)", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "134217728" {
		t.Fatalf("memory.max inside the scope = %q, want 134217728 (128M)", got)
	}
}

// Negative (planted spike): a step that tries to hold 1 GiB under MemoryMax 128M and no swap is
// killed, and only the step: the test process that started it keeps running and reads the
// step's failure. With MemoryMax alone the kernel swapped the hog out and it finished (measured
// 2026-10-17), so the swap bound is part of the containment.
func TestRun_Negative_SpikeKillsOnlyTheStep(t *testing.T) {
	requireScopes(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// MemoryHigh at MemoryMax: below it the kernel throttles the hog instead of killing it, and a
	// throttled step only ends at this test's deadline (measured 2026-10-17).
	step := Step{Label: "spike", Limits: Limits{MemoryHigh: "128M", MemoryMax: "128M", MemorySwapMax: "0"}, Stdout: &bytes.Buffer{}, Stderr: &errOut,
		Argv: []string{"env", hogEnv + "=1", self, "-test.run=^$"}}
	started := time.Now()
	runErr := Run(ctx, step)
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() == 0 || ctx.Err() != nil {
		t.Fatalf("the spiking step was not killed by its limit: %v, context %v, after %s (%s)", runErr, ctx.Err(), time.Since(started), errOut.String())
	}
	if os.Getpid() <= 0 {
		t.Fatal("unreachable: the test process is gone")
	}
}

// Negative: an empty command and limits that do not parse are refused before anything starts.
func TestRun_Negative_RefusedBeforeStart(t *testing.T) {
	if err := Run(context.Background(), Step{Label: "x", Limits: Limits{MemoryHigh: "8G", MemoryMax: "12G", MemorySwapMax: "0"}}); err == nil {
		t.Fatal("an empty command ran")
	}
	if err := Run(context.Background(), Step{Label: "x", Argv: []string{"true"}, Limits: Limits{MemoryHigh: "8", MemoryMax: "12G", MemorySwapMax: "0"}}); err == nil {
		t.Fatal("unparsable limits ran")
	}
}

// parseHeldDuration reads the elapsed wait a held refusal reports after "still held after".
func parseHeldDuration(t *testing.T, msg string) time.Duration {
	t.Helper()
	_, after, ok := strings.Cut(msg, "still held after ")
	if !ok {
		t.Fatalf("missing 'still held after' in %q", msg)
	}
	raw, _, ok := strings.Cut(after, " (")
	if !ok {
		t.Fatalf("missing duration delimiter in %q", msg)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("parse held duration %q in %q: %v", raw, msg, err)
	}
	return d
}

// Negative (planted): a held admission with --wait 2s refuses within 2s plus one poll and names
// the time actually waited rather than echoing MaxWait.
func TestWait_Negative_HeldWaitReportsElapsed(t *testing.T) {
	var log bytes.Buffer
	const waitBound, poll = 2 * time.Second, 600 * time.Millisecond
	started := time.Now()
	w := Waiter{
		Policy: policy, MaxWait: waitBound, Poll: poll, Sleep: SleepContext, Log: &log,
		Read: func() (sentinel.HostStats, error) {
			if time.Since(started) >= waitBound {
				time.Sleep(150 * time.Millisecond)
			}
			return host(4, 50, 10), nil
		},
	}
	_, err := w.Wait(t.Context(), "held-step")
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("always-held waiter admitted")
	}
	if elapsed < waitBound || elapsed > waitBound+poll {
		t.Fatalf("elapsed = %s, want within [%s, %s] (%v)", elapsed, waitBound, waitBound+poll, err)
	}
	reported := parseHeldDuration(t, err.Error())
	if (reported - elapsed).Abs() > 80*time.Millisecond {
		t.Fatalf("message reported %s, actual elapsed %s: %v", reported, elapsed, err)
	}
}

// deadlineShell replaces the scope check with a delayed refusal, isolating child execution from host systemd.
func deadlineShell(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH on %s: %v", runtime.GOOS, err)
	}
	dir := t.TempDir()
	testsupport.BuildExecutable(t, dir, "systemd-run", `package main
import ("os"; "time")
func main() { time.Sleep(500*time.Millisecond); os.Exit(1) }
`)
	t.Setenv("PATH", dir)
	return sh
}

// blockingDeadlineStep records child readiness before blocking on a private pipe until cancellation.
func blockingDeadlineStep(t *testing.T) (Step, *bytes.Buffer, string) {
	t.Helper()
	sh := deadlineShell(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cerr := w.Close(); cerr != nil {
			t.Error(cerr)
		}
		if cerr := r.Close(); cerr != nil {
			t.Error(cerr)
		}
	})
	ready := filepath.Join(t.TempDir(), "ready")
	errOut := new(bytes.Buffer)
	return Step{
		Label: "stdin-block", Limits: Limits{MemoryHigh: "96M", MemoryMax: "128M", MemorySwapMax: "0"},
		Stdin: r, Stdout: io.Discard, Stderr: errOut,
		Argv: []string{sh, "-c", `trap 'exit 143' TERM; printf ready > "$1"; read line`, "deadline-child", ready},
	}, errOut, ready
}

// requireDeadlineReport requires a real child start, a deadline error, and the promised diagnostic on both surfaces.
func requireDeadlineReport(t *testing.T, runErr error, diagnostic, ready string) {
	t.Helper()
	if marker, err := os.ReadFile(ready); err != nil || string(marker) != "ready" {
		t.Fatalf("blocking child never ready: marker=%q err=%v; run=%v", marker, err, runErr)
	}
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("child deadline error missing: %v", runErr)
	}
	for _, surface := range []string{runErr.Error(), diagnostic} {
		for _, want := range []string{"stdin-block", "sh", "deadline", "exceeded"} {
			if !strings.Contains(surface, want) {
				t.Fatalf("surface %q lacks %q", surface, want)
			}
		}
	}
}

// Negative (planted): the child starts after a slow scope check and is killed by its own execution deadline.
func TestRun_Negative_BlockingStdinKilledAtDeadline(t *testing.T) {
	step, errOut, ready := blockingDeadlineStep(t)
	const deadline = 300 * time.Millisecond
	step.Timeout = deadline
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	started := time.Now()
	runErr := Run(ctx, step)
	elapsed := time.Since(started)
	requireDeadlineReport(t, runErr, errOut.String(), ready)
	if elapsed > time.Second+deadline+util.CommandWaitDelay {
		t.Fatalf("blocking child err=%v after %s", runErr, elapsed)
	}
	if !strings.Contains(errOut.String(), "exceeded "+deadline.String()+" deadline") {
		t.Fatalf("step bound missing: %s", errOut.String())
	}
}

// Negative (planted): the caller's earlier deadline determines the reported execution bound after the scope check.
func TestRun_Negative_EarlierCallerDeadline(t *testing.T) {
	step, errOut, ready := blockingDeadlineStep(t)
	step.Timeout = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	runErr := Run(ctx, step)
	requireDeadlineReport(t, runErr, errOut.String(), ready)
	for _, surface := range []string{runErr.Error(), errOut.String()} {
		_, after, _ := strings.Cut(surface, "exceeded ")
		raw, _, _ := strings.Cut(after, " deadline")
		bound, err := time.ParseDuration(raw)
		maxBound := time.Second
		if runtime.GOOS == "linux" {
			maxBound = 600 * time.Millisecond
		}
		if err != nil || bound <= 0 || bound > maxBound {
			t.Fatalf("effective bound after scope check = %q: %v", raw, err)
		}
	}
}

// Negative (planted): a background caller and zero Timeout still expose the runner's default deadline to Run.
func TestRun_Negative_DefaultStepDeadline(t *testing.T) {
	step, errOut, ready := blockingDeadlineStep(t)
	runErr := Run(context.Background(), step)
	requireDeadlineReport(t, runErr, errOut.String(), ready)
	for _, surface := range []string{runErr.Error(), errOut.String()} {
		if !strings.Contains(surface, "exceeded "+util.DefaultCommandTimeout.String()+" deadline") {
			t.Fatalf("default bound missing: %s", surface)
		}
	}
}

// Positive: a short child (true) passes and a non-zero child's exit status passes through.
func TestRun_Positive_ShortChildAndExitStatus(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH on %s: %v", runtime.GOOS, err)
	}
	limits := Limits{MemoryHigh: "96M", MemoryMax: "128M", MemorySwapMax: "0"}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := Run(ctx, Step{Label: "short", Limits: limits, Stdout: io.Discard, Stderr: io.Discard, Argv: []string{"sh", "-c", "true"}}); err != nil {
		t.Fatalf("short child failed: %v", err)
	}
	err := Run(ctx, Step{Label: "code", Limits: limits, Stdout: io.Discard, Stderr: io.Discard, Argv: []string{"sh", "-c", "exit 7"}})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit 7 came back as %v", err)
	}
}

// Negative (planted): inside a controlling PTY (as in an agy lane sandbox), DefaultStep and Run
// must not let a backgrounded process group stop on SIGTTOU/SIGTTIN when the child initializes
// terminal line editing (libedit readline in pytest) or touches terminal attributes.
func TestRun_Negative_ControllingTTYDoesNotStop(t *testing.T) {
	t.Setenv("ADMISSION_TEST_PTY", "1")
	ptytest.Run(t, "TestAdmissionPTYHelper")
}

// TestAdmissionPTYHelper is the child process the controlling-terminal case re-executes; it does nothing unless ADMISSION_TEST_PTY is 1.
func TestAdmissionPTYHelper(t *testing.T) {
	if os.Getenv("ADMISSION_TEST_PTY") != "1" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMISSION_TEST_PTY_CHILD", "1")
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	step := DefaultStep("pty-step", []string{self, "-test.run=^TestAdmissionPTYChild$"}, Limits{MemoryHigh: "256M", MemoryMax: "512M", MemorySwapMax: "0"})
	if err := Run(ctx, step); err != nil {
		t.Fatalf("PTY step failed: %v", err)
	}
}

// TestAdmissionPTYChild re-executes terminal setup in the admitted background process group.
func TestAdmissionPTYChild(t *testing.T) {
	if os.Getenv("ADMISSION_TEST_PTY_CHILD") == "1" {
		ptytest.Check(t)
	}
}
