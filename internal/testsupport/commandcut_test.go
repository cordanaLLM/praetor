// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// slowGit is a git command that outlives any bound below two seconds: an alias that sleeps.
var slowGit = []string{"-c", "alias.slow=!sleep 2", "slow"}

// Positive (#1275): a git command its deadline killed is named as cut by the bound, with the time
// it ran, and the kill's own error stays wrapped. On Windows that error alone is a bare
// "exit status 1" with no output.
func TestFixtureCommandError_Positive_NamesTheBoundThatCutTheCommand(t *testing.T) {
	requireGit(t)
	const bound = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	start := time.Now()
	// Raw exec on purpose: FixtureCommandError classifies the bare *exec.ExitError of a context kill;
	// util.RunCommand wraps that error and signals the process group, which is not the case under test.
	out, runErr := exec.CommandContext(ctx, "git", slowGit...).CombinedOutput()
	err := FixtureCommandError(ctx, bound, time.Since(start), runErr)
	if err == nil || !strings.Contains(err.Error(), "cut by the 300ms fixture bound after ") {
		t.Fatalf("error %v (output %q) does not name the bound", err, out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("the kill's exit error is not wrapped: %v", err)
	}
}

// Negative: a git command that fails on its own within the bound keeps its error unchanged.
func TestFixtureCommandError_Negative_KeepsGitsOwnFailure(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Raw exec on purpose: the case is that FixtureCommandError leaves git's own bare error unchanged,
	// which util's diagnostic wrapping would already have changed.
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "refs/heads/does-not-exist")
	cmd.Dir = t.TempDir()
	cmd.Env = HermeticGitEnv(t)
	_, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatal("the fixture command was meant to fail")
	}
	if err := FixtureCommandError(ctx, 20*time.Second, time.Second, runErr); !errors.Is(err, runErr) || err.Error() != runErr.Error() {
		t.Fatalf("git's own failure was rewritten: %v", err)
	}
}

// Boundary: no error stays no error even past the deadline, and a cancelled context, which is not
// the bound, leaves the error as it is.
func TestFixtureCommandError_Boundary_OnlyTheDeadlineIsNamed(t *testing.T) {
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if err := FixtureCommandError(expired, time.Second, time.Second, nil); err != nil {
		t.Fatalf("a command that succeeded reads as failed: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	failure := errors.New("exit status 1")
	if err := FixtureCommandError(cancelled, time.Second, time.Second, failure); !errors.Is(err, failure) || err.Error() != failure.Error() {
		t.Fatalf("a cancellation was blamed on the bound: %v", err)
	}
}
