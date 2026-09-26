package util

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
}

// TestRunGitBytes_Positive_KeepsStreamsApartUnderRunGitsEnvironment proves the bounded form
// returns git's streams byte-exact and runs under RunGit's environment: an ambient GIT_DIR
// pointing elsewhere does not redirect it, as it does not redirect RunGit (BUG-886).
func TestRunGitBytes_Positive_KeepsStreamsApartUnderRunGitsEnvironment(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	if _, err := RunGit(ctx, repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere.git"))

	result, err := RunGitBytes(ctx, repo, 4096, "rev-parse", "--git-dir")
	if err != nil {
		t.Fatalf("RunGitBytes: %v (stderr %q)", err, result.Stderr)
	}
	if string(result.Stdout) != ".git\n" || len(result.Stderr) != 0 {
		t.Fatalf("streams = %q / %q, want the untrimmed repository git dir and no stderr", result.Stdout, result.Stderr)
	}
}

// TestRunGitBytes_Negative_FailureAndInvalidCap covers a failing git run, whose diagnostic
// stays on standard error, and caps RunCommandBytes refuses before starting anything.
func TestRunGitBytes_Negative_FailureAndInvalidCap(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outside := t.TempDir()
	// The ceiling stops discovery at the temp root, so an enclosing repository cannot answer.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(outside))

	result, err := RunGitBytes(ctx, outside, 4096, "rev-parse", "--verify", "HEAD")
	if err == nil {
		t.Fatal("rev-parse outside a repository succeeded")
	}
	// The diagnostic's wording follows the host locale, so only its stream is asserted.
	if len(result.Stdout) != 0 || len(strings.TrimSpace(string(result.Stderr))) == 0 {
		t.Fatalf("streams = %q / %q, want the diagnostic on stderr only", result.Stdout, result.Stderr)
	}
	for _, limit := range []int{0, -1, MaxCommandOutputBytes + 1} {
		if _, err := RunGitBytes(ctx, ".", limit, "version"); err == nil {
			t.Errorf("cap %d accepted", limit)
		}
	}
	var absent context.Context
	if _, err := RunGitBytes(absent, ".", 4096, "version"); err == nil {
		t.Error("nil context accepted")
	}
}

// TestRunGitBytes_Boundary_OutputCap pins the cap edge: output exactly at the cap is
// returned whole, one byte less is refused with the prefix kept as evidence.
func TestRunGitBytes_Boundary_OutputCap(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	full, err := RunGitBytes(ctx, ".", 4096, "version")
	if err != nil || len(full.Stdout) < 2 {
		t.Fatalf("git version: %q, %v", full.Stdout, err)
	}
	exact, err := RunGitBytes(ctx, ".", len(full.Stdout), "version")
	if err != nil || !bytes.Equal(exact.Stdout, full.Stdout) {
		t.Fatalf("output at the cap = %q, %v; want %q", exact.Stdout, err, full.Stdout)
	}
	short, err := RunGitBytes(ctx, ".", len(full.Stdout)-1, "version")
	if err == nil {
		t.Fatal("output one byte over the cap accepted")
	}
	if !bytes.Equal(short.Stdout, full.Stdout[:len(full.Stdout)-1]) {
		t.Fatalf("overflow evidence = %q, want the capped prefix of %q", short.Stdout, full.Stdout)
	}
}
