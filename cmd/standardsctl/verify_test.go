package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// verifyTestGitTimeout bounds one fixture git command (HISS-02).
const verifyTestGitTimeout = 30 * time.Second

// hermeticGitCommit runs one git command against dir under a hermetic environment: no
// inherited configuration or identity, so the fixture never depends on what is installed
// on the machine running the test.
func hermeticGitCommit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("fixture git environment: %v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, verifyTestGitTimeout)
	defer cancel()
	if out, runErr := util.RunGit(runCtx, dir, args...); runErr != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, runErr, out)
	}
}

func TestVerifyAll_Positive_PreservesTree(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	hermeticGitCommit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Create a .workingdir with no uncommitted changes
	if err := os.Mkdir(filepath.Join(dir, ".workingdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CI", "true")
	out, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v (out: %s)", err, out)
	}
}

func TestVerifyAll_Negative_AbortsOnUncommittedState(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	hermeticGitCommit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Create tracked file, commit it, then modify it to make tree dirty for git status --porcelain
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	hermeticGitCommit(t, dir, "add", "tracked.txt")
	hermeticGitCommit(t, dir, "commit", "-m", "add tracked")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("bar"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CI", "true")
	_, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err == nil {
		t.Fatal("expected error due to dirty tree in CI, got nil")
	}
	mustErrContain(t, err, "verify-all modified working tree")
}

// TestVerifyAll_Boundary_CIUnsetSkipsCheck confirms the gate is a no-op outside CI: a dirty,
// even non-git, directory must not fail the run when CI is unset.
func TestVerifyAll_Boundary_CIUnsetSkipsCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CI", "")

	out, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err != nil {
		t.Fatalf("expected no-op with CI unset, got error: %v (out: %s)", err, out)
	}
}

// TestVerifyAll_Boundary_NonGitDirFailsClosed confirms a directory git does not recognize
// fails the gate instead of silently reporting a clean tree.
func TestVerifyAll_Boundary_NonGitDirFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CI", "true")

	_, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err == nil {
		t.Fatal("expected error for a non-git directory, got nil")
	}
	mustErrContain(t, err, "git status failed")
}
