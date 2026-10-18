// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/backlogcap"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func setupStateWorktreeFixture(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	mainRepo := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "praetor-test"},
		{"config", "user.email", "test@example.com"},
	} {
		if out, err := runFixtureGit(t, mainRepo, env, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFixtureFile(t, mainRepo, "README.md", "# Main\n")
	writeFixtureFile(t, mainRepo, ".gitignore", "/.workingdir/\n")
	if out, err := runFixtureGit(t, mainRepo, env, "add", "."); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := runFixtureGit(t, mainRepo, env, "commit", "-q", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if err := dispatchCommand("state", []string{"init", mainRepo}); err != nil {
		t.Fatalf("state init in mainRepo: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "add", "planted-main-task", mainRepo}); err != nil {
		t.Fatalf("state task add in mainRepo: %v", err)
	}

	linkedWT := filepath.Join(t.TempDir(), "wt-state")
	if out, err := runFixtureGit(t, mainRepo, env, "worktree", "add", "-b", "feature/wt", linkedWT); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	return mainRepo, linkedWT
}

func TestStateStatus_LinkedWorktreeWithoutWorkingDir(t *testing.T) {
	mainRepo, linkedWT := setupStateWorktreeFixture(t)

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"status", linkedWT})
	})
	if err != nil {
		t.Fatalf("state status failed: %v\n%s", err, out)
	}
	requireNoticeNames(t, out, filepath.Join(mainRepo, ".workingdir"))
	if !strings.Contains(out, "1 open, 0 completed") {
		t.Fatalf("expected planted task count in output:\n%s", out)
	}
	if !strings.Contains(out, "Branch:            feature/wt") {
		t.Fatalf("expected linked worktree branch feature/wt in output:\n%s", out)
	}

	taskListOut, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"task", "list", linkedWT})
	})
	if err != nil {
		t.Fatalf("state task list failed: %v\n%s", err, taskListOut)
	}
	if !strings.Contains(taskListOut, "planted-main-task") {
		t.Fatalf("state task list did not show main task: %s", taskListOut)
	}
}

func TestStateStatus_LinkedWorktreeWithOwnWorkingDir(t *testing.T) {
	_, linkedWT := setupStateWorktreeFixture(t)

	if err := dispatchCommand("state", []string{"init", linkedWT}); err != nil {
		t.Fatalf("state init in linkedWT: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "add", "planted-wt-task", linkedWT}); err != nil {
		t.Fatalf("state task add in linkedWT: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "add", "second-wt-task", linkedWT}); err != nil {
		t.Fatalf("state task add in linkedWT: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"status", linkedWT})
	})
	if err != nil {
		t.Fatalf("state status failed: %v\n%s", err, out)
	}
	noticePrefix := "state: using the main worktree's .workingdir at "
	if strings.Contains(out, noticePrefix) {
		t.Fatalf("output must not contain notice when worktree has own .workingdir:\n%s", out)
	}
	if !strings.Contains(out, "2 open, 0 completed") {
		t.Fatalf("expected own task count (2 open) in output:\n%s", out)
	}
}

func TestStateStatus_MainCheckoutWithoutWorkingDir(t *testing.T) {
	dir := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	if _, err := runFixtureGit(t, dir, env, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	err := dispatchCommand("state", []string{"status", dir})
	if err == nil {
		t.Fatal("expected state status to fail on main checkout without .workingdir")
	}
	want := "state status: .workingdir/ does not exist in " + dir + " (run 'praetorctl state init " + dir + "' first)"
	if err.Error() != want {
		t.Fatalf("want error %q, got %q", want, err.Error())
	}
}

func TestBacklogCap_LinkedWorktreeManifestDiffersFromMain(t *testing.T) {
	mainRepo, linkedWT := setupStateWorktreeFixture(t)

	mainManifest := "version: 1\nbacklog:\n  caps:\n    questions:\n      max: 5\n      action: gate\n"
	writeFixtureFile(t, mainRepo, config.ManifestFileName, mainManifest)

	wtManifest := "version: 1\nbacklog:\n  caps:\n    defects:\n      max: 1\n      action: gate\n"
	writeFixtureFile(t, linkedWT, config.ManifestFileName, wtManifest)

	if err := dispatchCommand("state", []string{"bug", "add", "--title=planted-defect-1", "--dir=" + mainRepo}); err != nil {
		t.Fatalf("state bug add 1 in mainRepo: %v", err)
	}
	if err := dispatchCommand("state", []string{"bug", "add", "--title=planted-defect-2", "--dir=" + mainRepo}); err != nil {
		t.Fatalf("state bug add 2 in mainRepo: %v", err)
	}

	policy, _, err := config.LoadUnadoptedEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: linkedWT})
	if err != nil {
		t.Fatalf("load effective policy in linkedWT: %v", err)
	}

	report, err := backlogcap.Evaluate(t.Context(), linkedWT, policy)
	if err != nil {
		t.Fatalf("backlogcap.Evaluate in linkedWT: %v", err)
	}

	if len(report.Categories) != 1 {
		t.Fatalf("expected 1 category from worktree manifest, got %d", len(report.Categories))
	}
	cat := report.Categories[0]
	if cat.Name != "defects" {
		t.Fatalf("expected category defects from worktree manifest, got %q", cat.Name)
	}
	if cat.Count() != 2 {
		t.Fatalf("expected 2 defects from shared ledger, got %d", cat.Count())
	}
	if cat.State() != backlogcap.StateOver {
		t.Fatalf("expected category to be OVER the cap, got %s", cat.State())
	}
}

func TestStateSync_LinkedWorktree(t *testing.T) {
	mainRepo, linkedWT := setupStateWorktreeFixture(t)

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"sync", linkedWT})
	})
	if err != nil {
		t.Fatalf("state sync failed: %v\n%s", err, out)
	}

	requireNoticeNames(t, out, filepath.Join(mainRepo, ".workingdir"))

	verifyOut, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"sync", "--verify", linkedWT})
	})
	if err != nil {
		t.Fatalf("state sync --verify failed: %v\n%s", err, verifyOut)
	}

	stateContent := readFixtureFile(t, mainRepo, ".workingdir/STATE.md")
	if !strings.Contains(stateContent, "on `feature/wt`") {
		t.Fatalf("main ledger STATE.md does not name worktree branch:\n%s", stateContent)
	}
}

// noticeDir returns the directory the main-worktree notice in out names, or "" when out holds none.
func noticeDir(out string) string {
	const prefix = "state: using the main worktree's .workingdir at "
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return rest
		}
	}
	return ""
}

// sameDir reports whether two paths name one directory on disk. git reports resolved paths
// (/private/var on macOS, long names on Windows) where t.TempDir() hands out the spelling the
// platform's temporary folder has, so the notice is compared by directory, not by string.
func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// requireNoticeNames fails t unless out holds the main-worktree notice and it names want.
func requireNoticeNames(t *testing.T, out, want string) {
	t.Helper()
	if got := noticeDir(out); got == "" || !sameDir(got, want) {
		t.Fatalf("output missing notice naming %s (got %q):\n%s", want, got, out)
	}
}

// Boundary: a notice spelling the same directory another way (here with a .. step, on macOS
// /private/var for /var) passes; a notice naming another directory, or none, fails.
func TestNoticeNames_SameDirectoryOtherSpelling(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "main", ".workingdir")
	other := filepath.Join(root, "other", ".workingdir")
	for _, dir := range []string{want, other} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	notice := func(dir string) string { return "x\nstate: using the main worktree's .workingdir at " + dir + "\ny\n" }
	respelled := root + string(filepath.Separator) + "other" + string(filepath.Separator) + ".." + string(filepath.Separator) + "main" + string(filepath.Separator) + ".workingdir"
	if got := noticeDir(notice(respelled)); got != respelled || !sameDir(got, want) {
		t.Fatalf("a respelled path to the same directory must pass: %q", got)
	}
	if sameDir(noticeDir(notice(other)), want) {
		t.Fatal("a notice naming another directory must fail")
	}
	if noticeDir("no notice here\n") != "" {
		t.Fatal("output without the notice must name no directory")
	}
}
