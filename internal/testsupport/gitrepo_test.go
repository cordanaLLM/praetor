// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: the repository exists and its origin remote is the one given.
func TestInitGitRepoWithOrigin_Positive_SetsOrigin(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "https://github.com/acme/widget.git")
	out, err := runGit(t, dir, HermeticGitEnv(t), "config", "--get", "remote.origin.url")
	if err != nil || out != "https://github.com/acme/widget.git" {
		t.Fatalf("origin = %q (err %v), want the given remote", out, err)
	}
}

// Negative: an empty origin leaves the repository without one; git config answers exit 1.
func TestInitGitRepoWithOrigin_Negative_EmptyOriginAddsNone(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("no repository was initialized: %v", err)
	}
	_, err := runGit(t, dir, HermeticGitEnv(t), "config", "--get", "remote.origin.url")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("origin lookup = %v, want git's not-set answer (exit 1)", err)
	}
}

// Boundary: an inherited GIT_DIR naming another repository does not redirect the fixture.
func TestInitGitRepoWithOrigin_Boundary_IgnoresInheritedGitDir(t *testing.T) {
	requireGit(t)
	outer := hostileGitEnvironment(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "git@github.com:acme/widget.git")
	if _, err := os.Stat(filepath.Join(dir, ".git", "config")); err != nil {
		t.Fatalf("the fixture repository was not created in dir: %v", err)
	}
	if out, err := runGit(t, outer, HermeticGitEnv(t), "config", "--get", "remote.origin.url"); err == nil {
		t.Fatalf("the origin landed in the inherited repository: %q", out)
	}
}

// Positive: the origin HEAD names the given branch, as a clone records it.
func TestRecordOriginHead_Positive_PointsAtTheBranch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "https://github.com/acme/widget.git")
	RecordOriginHead(t, dir, "master")
	out, err := runGit(t, dir, HermeticGitEnv(t), "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil || out != "refs/remotes/origin/master" {
		t.Fatalf("origin HEAD = %q (err %v), want refs/remotes/origin/master", out, err)
	}
}

// Negative: outside a repository git refuses, and the refusal fails the test with git's answer.
func TestRecordOriginHead_Negative_OutsideARepositoryFails(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	message := runRecorded(t, func(tb testing.TB) { RecordOriginHead(tb, dir, "master") })
	if !strings.Contains(message, "testsupport: git [symbolic-ref") {
		t.Fatalf("a refused origin HEAD was not reported: %q", message)
	}
}

// Boundary: a branch with a slash is recorded whole, and recording again moves the origin HEAD.
func TestRecordOriginHead_Boundary_SlashBranchAndRerecord(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")
	for _, branch := range []string{"release/stable", "trunk"} {
		RecordOriginHead(t, dir, branch)
		out, err := runGit(t, dir, HermeticGitEnv(t), "symbolic-ref", "refs/remotes/origin/HEAD")
		if err != nil || out != "refs/remotes/origin/"+branch {
			t.Fatalf("origin HEAD = %q (err %v), want refs/remotes/origin/%s", out, err, branch)
		}
	}
}

// Positive: RunFixtureGit runs every command in order and returns what the last one printed, here
// the id of the commit the earlier commands made.
func TestRunFixtureGit_Positive_ReturnsTheLastOutput(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	head := RunFixtureGit(t, dir, []string{"add", "a.txt"}, []string{"commit", "--quiet", "-m", "a"},
		[]string{"rev-parse", "HEAD"})
	want, err := runGit(t, dir, HermeticGitEnv(t), "log", "-1", "--format=%H")
	if err != nil || head != want || len(head) < 40 {
		t.Fatalf("RunFixtureGit returned %q, want the commit id %q (err %v)", head, want, err)
	}
}

// Negative: a command git refuses fails the test with git's answer, and nothing after it runs.
func TestRunFixtureGit_Negative_RefusedCommandFailsTheTest(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")
	RunFixtureGit(t, dir, []string{"commit", "--quiet", "--allow-empty", "-m", "root"})
	message := runRecorded(t, func(tb testing.TB) {
		RunFixtureGit(tb, dir, []string{"rev-parse", "--verify", "refs/heads/absent"}, []string{"tag", "after"})
	})
	if !strings.Contains(message, "testsupport: git [rev-parse --verify refs/heads/absent]") {
		t.Fatalf("a refused command was not reported: %q", message)
	}
	if out, err := runGit(t, dir, HermeticGitEnv(t), "tag", "--list"); err != nil || out != "" {
		t.Fatalf("a command after the refused one still ran: tags %q (err %v)", out, err)
	}
}

// Boundary: no commands run nothing and return an empty output.
func TestRunFixtureGit_Boundary_NoCommandsReturnEmpty(t *testing.T) {
	requireGit(t)
	if out := RunFixtureGit(t, t.TempDir()); out != "" {
		t.Fatalf("RunFixtureGit with no commands returned %q", out)
	}
}
