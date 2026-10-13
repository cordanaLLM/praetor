package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// #1275: the adoption fixture's git add failed once on the Windows runner with a bare
// "exit status 1" and no output. A git cut by the fixture bound reads exactly so on Windows,
// so the fixture runner now names the bound, and a failed add prints the porcelain status.

// Positive: a git the bound cut is reported as cut, not as a silent git failure.
func TestRunFixtureGitWithin_Positive_NamesTheBoundThatCutGit(t *testing.T) {
	out, err := runFixtureGitWithin(300*time.Millisecond, t.TempDir(), testsupport.HermeticGitEnv(t),
		"-c", "alias.slow=!sleep 2", "slow")
	if err == nil || !strings.Contains(err.Error(), "cut by the 300ms fixture bound") {
		t.Fatalf("error %v (output %q) does not name the bound", err, out)
	}
}

// Negative: git failing within the bound keeps git's own error and output.
func TestRunFixtureGitWithin_Negative_KeepsGitsOwnFailure(t *testing.T) {
	out, err := runFixtureGit(t, t.TempDir(), testsupport.HermeticGitEnv(t), "rev-parse", "--verify", "refs/heads/does-not-exist")
	if err == nil || strings.Contains(err.Error(), "cut by") || out == "" {
		t.Fatalf("error %v, output %q; want git's own failure with its message", err, out)
	}
}

// Boundary: the status a failed add prints names the fixture's paths, or says it could not.
func TestFixtureStatus_Boundary_NamesThePathsOrWhyNot(t *testing.T) {
	env := testsupport.HermeticGitEnv(t)
	repo := t.TempDir()
	if out, err := runFixtureGit(t, repo, env, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "written.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fixtureStatus(t, repo, env); !strings.Contains(got, "?? written.txt") {
		t.Fatalf("status %q does not name the untracked file", got)
	}
	notRepo := filepath.Join(t.TempDir(), "missing")
	if got := fixtureStatus(t, notRepo, env); !strings.Contains(got, "git status --porcelain failed too") {
		t.Fatalf("status %q does not say why it is missing", got)
	}
}
