//go:build unix

// SIGTERM-first cancellation applies to Unix process groups; on Windows cancellation
// terminates the direct child at once (command_bytes_other.go).

package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cancelWhenReady cancels once path exists, polling within a bound. It reports nothing, so it
// can run in a goroutine of its own; the caller asserts what the command did.
func cancelWhenReady(path string, cancel context.CancelFunc) {
	const attempts = 600
	for range attempts {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
}

// A command whose context ends is asked to stop with SIGTERM, so it removes its lock the way
// git removes index.lock, instead of being killed with the lock left behind.
func TestRunCommand_Positive_CancellationLetsCommandCleanUp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	go cancelWhenReady(filepath.Join(dir, "ready"), cancel)
	began := time.Now()
	_, err := RunCommand(ctx, dir, "sh", "-c", cleanupChildScript)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reported: %v", err)
	}
	if took := time.Since(began); took >= CommandWaitDelay {
		t.Fatalf("the call took %v although the command stopped at once", took)
	}
	assertCleanedUp(t, dir)
}

// An output overflow cancels the command the same way, so it too gets to clean up.
func TestRunCommandBytes_Positive_OutputBoundLetsCommandCleanUp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	script := "trap 'rm -f lock; touch cleaned; exit 130' TERM; : > lock; head -c 64 /dev/zero; " +
		"sleep 30 >/dev/null 2>&1 & wait $!"
	_, err := RunCommandBytes(ctx, dir, "sh", 16, "-c", script)
	if err == nil || !strings.Contains(err.Error(), "exceeds 16 bytes") {
		t.Fatalf("overflow not reported: %v", err)
	}
	assertCleanedUp(t, dir)
}

// A command that ignores SIGTERM still ends: exec.Cmd kills it CommandWaitDelay after its
// deadline, the call returns, and no member of its group survives it.
func TestRunCommand_Negative_StubbornCommandKilledAfterWaitDelay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const deadline = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	began := time.Now()
	_, err := RunCommand(ctx, dir, "sh", "-c", stubbornChildScript)
	took := time.Since(began)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not reported: %v", err)
	}
	if took < CommandWaitDelay {
		t.Fatalf("the command was killed after %v, before its %v grace", took, CommandWaitDelay)
	}
	if bound := deadline + CommandWaitDelay + 10*time.Second; took > bound {
		t.Fatalf("the call took %v, past its %v bound", took, bound)
	}
	awaitGroupGone(t, dir)
}
