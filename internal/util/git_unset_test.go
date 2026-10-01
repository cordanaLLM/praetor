// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// gitUnsetProbeTimeout bounds one git call that makes an exit status for the
// GitAnsweredUnset tests (HISS-02).
const gitUnsetProbeTimeout = 30 * time.Second

// missingKeyArgs ask git config for a key the fixture's empty file does not set, which
// git answers with exit status 1.
var missingKeyArgs = []string{"config", "--file", "empty.cfg", "--get", "no.such.key"}

// gitUnsetDir returns a directory holding an empty empty.cfg that is not inside any
// repository, with git's own configuration kept out. It skips the test when git is not
// installed: git makes every exit status these tests need, so they run wherever git
// does, with no shell (HISS-21).
func gitUnsetDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// Repository discovery stops at dir, so a temporary directory inside a checkout still
	// reads as "not a git repository".
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if err := os.WriteFile(filepath.Join(dir, "empty.cfg"), nil, 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	return dir
}

// gitExitError runs git with args in dir under exec.CommandContext and returns its error,
// failing the test unless git exited with status want.
func gitExitError(t *testing.T, dir string, want int, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), gitUnsetProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
		t.Fatalf("git %v: got %v, want exit status %d: %s", args, err, want, out)
	}
	return err
}

func TestGitAnsweredUnset_Positive_MissingKeyUnderLiveContext(t *testing.T) {
	dir := gitUnsetDir(t)
	if err := gitExitError(t, dir, 1, missingKeyArgs...); !GitAnsweredUnset(context.Background(), err) {
		t.Fatalf("exit status 1 under a live context must read as unset: %v", err)
	}
	// RunGit is how callers reach git; the error it wraps must answer the same way.
	ctx, cancel := context.WithTimeout(context.Background(), gitUnsetProbeTimeout)
	defer cancel()
	if _, err := RunGit(ctx, dir, missingKeyArgs...); !GitAnsweredUnset(ctx, err) {
		t.Fatalf("RunGit's exit status 1 must read as unset: %v", err)
	}
}

func TestGitAnsweredUnset_Boundary_ContextAndWrapping(t *testing.T) {
	err1 := gitExitError(t, gitUnsetDir(t), 1, missingKeyArgs...)
	var nilCtx context.Context
	if !GitAnsweredUnset(nilCtx, err1) {
		t.Error("exit status 1 with a nil context must read as unset")
	}
	if !GitAnsweredUnset(context.Background(), fmt.Errorf("read: %w", err1)) {
		t.Error("a wrapped exit status 1 must read as unset")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if GitAnsweredUnset(cancelled, err1) {
		t.Error("a cancelled context is never an answer, whatever the exit status")
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if GitAnsweredUnset(expired, err1) {
		t.Error("an expired context is never an answer, whatever the exit status")
	}
}

func TestGitAnsweredUnset_Negative_OtherFailuresAreNotAnswers(t *testing.T) {
	live := context.Background()
	if GitAnsweredUnset(live, nil) {
		t.Error("a nil error must not read as unset")
	}
	if GitAnsweredUnset(live, errors.New("arbitrary error")) {
		t.Error("an error without an exit status must not read as unset")
	}
	dir := gitUnsetDir(t)
	// rev-parse outside any repository exits 128.
	if err := gitExitError(t, dir, 128, "rev-parse", "--git-dir"); GitAnsweredUnset(live, err) {
		t.Errorf("exit status 128 must not read as unset: %v", err)
	}
	// An invalid value pattern exits 6.
	invalidPattern := append(append([]string{}, missingKeyArgs...), "[")
	if err := gitExitError(t, dir, 6, invalidPattern...); GitAnsweredUnset(live, err) {
		t.Errorf("exit status 6 must not read as unset: %v", err)
	}
}
