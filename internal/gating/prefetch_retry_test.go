// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// toolingTimeoutProbe records go command invocations across root and tooling modules,
// timing out the first tooling download to exercise retry handling.
type toolingTimeoutProbe struct {
	repoDir         string
	toolingDir      string
	downloadCalls   int
	rootVerifyCalls int
	retryDeadlines  []time.Time
}

// run executes injected go commands, failing the first tooling download with a timeout
// and returning recorded command outputs or errors.
func (p *toolingTimeoutProbe) run(ctx context.Context, dir, _ string, args ...string) (string, error) {
	if dir == p.repoDir && isGoModSubcommand(args, "verify") {
		p.rootVerifyCalls++
	}
	if dir != p.toolingDir || !isGoModSubcommand(args, "download") {
		return "", nil
	}
	p.downloadCalls++
	if deadline, ok := ctx.Deadline(); ok {
		p.retryDeadlines = append(p.retryDeadlines, deadline)
	}
	if p.downloadCalls == 1 {
		return "downloading modules", fmt.Errorf("download timed out: %w", context.DeadlineExceeded)
	}
	return "", nil
}

// isGoModSubcommand reports whether args represents a two-element go mod invocation for sub.
func isGoModSubcommand(args []string, sub string) bool {
	return len(args) == 2 && args[1] == sub
}

// failFirstDownloadRunner returns a commandRunner that returns firstErr on the first
// download invocation and succeeds thereafter, along with a pointer to the call counter.
func failFirstDownloadRunner(firstOut string, firstErr error) (commandRunner, *int) {
	var downloadCalls int
	run := func(_ context.Context, _, _ string, args ...string) (string, error) {
		if !isGoModSubcommand(args, "download") {
			return "", nil
		}
		downloadCalls++
		if downloadCalls == 1 {
			return firstOut, firstErr
		}
		return "", nil
	}
	return run, &downloadCalls
}

// assertPassedAfterRetry asserts that prefetch succeeded on its second download attempt
// and recorded the expected transient failure reason in its report.
func assertPassedAfterRetry(t *testing.T, rep *PrefetchReport, err error, downloadCalls int, wantReason string) {
	t.Helper()
	if err != nil || rep == nil {
		t.Fatalf("expected SIGKILL prefetch error to retry and pass, got rep=%+v err=%v", rep, err)
	}
	if downloadCalls != 2 {
		t.Errorf("downloadCalls = %d, want 2", downloadCalls)
	}
	wantSub := fmt.Sprintf("passed after retry (first attempt: %s)", wantReason)
	if !strings.Contains(rep.Message, wantSub) {
		t.Errorf("rep.Message = %q, want %s retry note", rep.Message, wantReason)
	}
}

// TestPrefetchRetry_Positive_DownloadTimeoutRetriedOnce plants a transient download timeout on the tooling module and expects a successful retry.
func TestPrefetchRetry_Positive_DownloadTimeoutRetriedOnce(t *testing.T) {
	repo, cfg, _ := trackedModuleFixture(t, true)
	probe := &toolingTimeoutProbe{
		repoDir:    repo,
		toolingDir: filepath.Join(repo, "tooling"),
	}
	cfg.run = probe.run

	msg, err := runGoPrefetch(t.Context(), cfg)
	if err != nil {
		t.Fatalf("expected retry to recover transient download timeout, got error: %v", err)
	}
	wantSub := "tooling/go.mod: passed after retry (first attempt: context deadline exceeded)"
	if !strings.Contains(msg, wantSub) {
		t.Errorf("moduleResultsMessage = %q, want substring %q", msg, wantSub)
	}
	if probe.rootVerifyCalls != 1 {
		t.Errorf("root module verify calls = %d, want 1 (same module retried only)", probe.rootVerifyCalls)
	}
	if probe.downloadCalls != 2 || len(probe.retryDeadlines) != 2 {
		t.Fatalf("tooling download calls = %d (deadlines %d), want 2", probe.downloadCalls, len(probe.retryDeadlines))
	}
}

// TestPrefetchRetry_Positive_TwoModulesEachRetriedOnce plants transient download timeouts across two modules and expects both to recover on retry.
func TestPrefetchRetry_Positive_TwoModulesEachRetriedOnce(t *testing.T) {
	repo, cfg, _ := trackedModuleFixture(t, true)
	toolingDir := filepath.Join(repo, "tooling")
	downloadCalls := map[string]int{}

	cfg.run = func(_ context.Context, dir, _ string, args ...string) (string, error) {
		if isGoModSubcommand(args, "download") {
			downloadCalls[dir]++
			if downloadCalls[dir] == 1 {
				return "downloading modules", fmt.Errorf("download timed out: %w", context.DeadlineExceeded)
			}
		}
		return "", nil
	}

	msg, err := runGoPrefetch(t.Context(), cfg)
	if err != nil {
		t.Fatalf("expected both modules to recover after one retry each, got error: %v", err)
	}
	if downloadCalls[repo] != 2 || downloadCalls[toolingDir] != 2 {
		t.Fatalf("download calls = root:%d tooling:%d, want 2 each", downloadCalls[repo], downloadCalls[toolingDir])
	}
	for _, wantSub := range []string{
		"go.mod: passed after retry (first attempt: context deadline exceeded)",
		"tooling/go.mod: passed after retry (first attempt: context deadline exceeded)",
	} {
		if !strings.Contains(msg, wantSub) {
			t.Errorf("moduleResultsMessage = %q, want substring %q", msg, wantSub)
		}
	}
}

// TestPrefetchRetry_Negative_TimeoutTwiceFailsNamingBothAttempts plants repeated download timeouts and expects failure after two attempts naming both errors.
func TestPrefetchRetry_Negative_TimeoutTwiceFailsNamingBothAttempts(t *testing.T) {
	goDir := newGoModuleDir(t)
	downloadCalls := 0
	stub := func(_ context.Context, _, _ string, args ...string) (string, error) {
		if isGoModSubcommand(args, "download") {
			downloadCalls++
			return fmt.Sprintf("attempt-%d-out", downloadCalls), fmt.Errorf("attempt %d: %w", downloadCalls, context.DeadlineExceeded)
		}
		return "", nil
	}

	rep, err := prefetchDependencies(t.Context(), goDir, stub)
	if rep != nil || err == nil {
		t.Fatalf("expected error after two transient timeouts, got rep=%+v err=%v", rep, err)
	}
	if downloadCalls != 2 {
		t.Errorf("download call count = %d, want 2", downloadCalls)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded in error chain, got %v", err)
	}
	for _, want := range []string{"first attempt", "second attempt", "attempt-1-out", "attempt-2-out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestPrefetchRetry_Negative_NonTransientErrorNotRetried plants a permanent exit error and expects immediate failure without a retry.
func TestPrefetchRetry_Negative_NonTransientErrorNotRetried(t *testing.T) {
	goDir := newGoModuleDir(t)
	nonTransient := errors.New("exit status 1")
	calls := 0
	stub := func(_ context.Context, _, _ string, args ...string) (string, error) {
		calls++
		if isGoModSubcommand(args, "download") {
			return "checksum mismatch", nonTransient
		}
		return "", nil
	}

	rep, err := prefetchDependencies(t.Context(), goDir, stub)
	if rep != nil || !errors.Is(err, nonTransient) {
		t.Fatalf("expected non-transient failure, got rep=%+v err=%v", rep, err)
	}
	if calls != 2 {
		t.Errorf("stub call count = %d, want 2 (verify + download once, no retry)", calls)
	}
	const wantErr = "dependency download failed: checksum mismatch (exit status 1)"
	if err.Error() != wantErr {
		t.Errorf("error = %q, want %q", err.Error(), wantErr)
	}
}

// TestPrefetchRetry_Boundary_ParentContextCancelledNoRetry plants a cancelled parent context and expects immediate cancellation without retrying.
func TestPrefetchRetry_Boundary_ParentContextCancelledNoRetry(t *testing.T) {
	goDir := newGoModuleDir(t)

	// Already cancelled before prefetch starts -> 0 calls, no retry, names cancel.
	preCancelled, preCancel := context.WithCancel(t.Context())
	preCancel()
	preCalls := 0
	_, err := prefetchDependencies(preCancelled, goDir, func(context.Context, string, string, ...string) (string, error) {
		preCalls++
		return "", context.DeadlineExceeded
	})
	if preCalls != 0 || err == nil || !strings.Contains(err.Error(), "cancel") || !errors.Is(err, context.Canceled) {
		t.Errorf("pre-cancelled parent ctx: calls=%d err=%v", preCalls, err)
	}

	// Cancelled during the first command -> 1 call, no retry, names cancel.
	midCtx, midCancel := context.WithCancel(t.Context())
	defer midCancel()
	midCalls := 0
	_, err = prefetchDependencies(midCtx, goDir, func(context.Context, string, string, ...string) (string, error) {
		midCalls++
		midCancel()
		return "", errors.New("signal: killed")
	})
	if midCalls != 1 || err == nil || !strings.Contains(err.Error(), "cancel") || !errors.Is(err, context.Canceled) {
		t.Errorf("mid-cancelled parent ctx: calls=%d err=%v", midCalls, err)
	}
}

// killedBySignalExitError produces a genuine SIGKILL *exec.ExitError by running
// a self-killing shell command, returning the resulting exit error.
func killedBySignalExitError(t *testing.T) *exec.ExitError {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX signal status on Windows: a SIGKILL ExitError cannot be produced")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, runErr := util.RunCommand(ctx, "", "sh", "-c", "kill -KILL $$")
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr == nil {
		t.Fatalf("expected *exec.ExitError from SIGKILL process, got %T (%v)", runErr, runErr)
	}
	return exitErr
}

// TestPrefetchRetry_Positive_KilledBySignalRetried plants a process SIGKILL exit error on download and expects a successful retry.
func TestPrefetchRetry_Positive_KilledBySignalRetried(t *testing.T) {
	goDir := newGoModuleDir(t)
	exitErr := killedBySignalExitError(t)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "raw ExitError", err: exitErr},
		{name: "wrapped ExitError", err: fmt.Errorf("process killed: %w", exitErr)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, downloadCalls := failFirstDownloadRunner("", tc.err)
			rep, err := prefetchDependencies(t.Context(), goDir, run)
			assertPassedAfterRetry(t, rep, err, *downloadCalls, "signal: killed")
		})
	}
}

// TestIsTransientPrefetchError_3D plants 3D error variants and expects exact transient classification across all cases.
func TestIsTransientPrefetchError_3D(t *testing.T) {
	exitErr := killedBySignalExitError(t)
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "deadline exceeded", err: fmt.Errorf("download: %w", context.DeadlineExceeded), want: true},
		{name: "sigkill ExitError", err: exitErr, want: true},
		{name: "wrapped sigkill ExitError", err: fmt.Errorf("verify: %w", exitErr), want: true},
		{name: "exit 137 text", err: errors.New("exit status 137"), want: true},
		{name: "exit 143 text", err: errors.New("exit status 143"), want: true},
		{name: "signal terminated text", err: errors.New("signal: terminated"), want: true},
		{name: "plain exit 1", err: errors.New("checksum mismatch (exit status 1)"), want: false},
		{name: "context canceled", err: context.Canceled, want: false},
	}
	for _, tc := range cases {
		if got := isTransientPrefetchError(tc.err); got != tc.want {
			t.Errorf("%s: isTransientPrefetchError(%v) = %t, want %t", tc.name, tc.err, got, tc.want)
		}
	}
}
