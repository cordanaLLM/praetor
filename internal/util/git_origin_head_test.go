// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// originHeadRepo initialises a work tree and points refs/remotes/origin/HEAD at target, unless
// target is empty, or skips the test when git is unavailable (HISS-21: the reader asks git, and a
// host without git cannot answer). The target branch need not exist: git clone records the
// symbolic ref whether or not the branch was fetched yet.
func originHeadRepo(t *testing.T, target string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := t.TempDir()
	if _, err := RunGit(t.Context(), dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if target != "" {
		if _, err := RunGit(t.Context(), dir, "symbolic-ref", originHeadRef, target); err != nil {
			t.Fatalf("git symbolic-ref: %v", err)
		}
	}
	return dir
}

func TestReadOriginHeadBranch_Positive_NamesTheRecordedBranch(t *testing.T) {
	for _, branch := range []string{"main", "master", "release/stable"} {
		dir := originHeadRepo(t, originBranchPrefix+branch)
		got, ok, err := ReadOriginHeadBranch(t.Context(), dir)
		if err != nil || !ok || got != branch {
			t.Fatalf("origin HEAD at %s: got %q, %v, %v", branch, got, ok, err)
		}
		// A subdirectory of the checkout reads the same ref.
		sub := filepath.Join(dir, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := ReadOriginHeadBranch(t.Context(), sub); err != nil || !ok || got != branch {
			t.Fatalf("subdirectory of origin HEAD at %s: got %q, %v, %v", branch, got, ok, err)
		}
	}
}

// No checkout, a .git directory git does not take for a repository and a checkout that never
// recorded origin HEAD all answer "none", not an error.
func TestReadOriginHeadBranch_Negative_NoneRecordedIsNotAnError(t *testing.T) {
	if got, ok, err := ReadOriginHeadBranch(t.Context(), originHeadRepo(t, "")); err != nil || ok || got != "" {
		t.Fatalf("a checkout without origin HEAD: got %q, %v, %v", got, ok, err)
	}
	if got, ok, err := ReadOriginHeadBranch(t.Context(), t.TempDir()); err != nil || ok || got != "" {
		t.Fatalf("a directory outside any checkout: got %q, %v, %v", got, ok, err)
	}
	stub := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stub, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := ReadOriginHeadBranch(t.Context(), stub); err != nil || ok || got != "" {
		t.Fatalf("a .git directory git does not take for a repository: got %q, %v, %v", got, ok, err)
	}
}

// Only git's own "not a git repository" exit is that answer: a success, a failure that is no git
// exit, and a git exit with another message are not.
func TestGitAnsweredNotARepository_3D(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	outside, err := RunGitProbe(t.Context(), t.TempDir(), maxOriginHeadBytes, "rev-parse", "--git-dir")
	if !GitAnsweredNotARepository(outside, err) {
		t.Fatalf("git's answer outside a repository must be recognised: %q, %v", outside.Stderr, err)
	}
	inside := originHeadRepo(t, "")
	result, err := RunGitProbe(t.Context(), inside, maxOriginHeadBytes, "rev-parse", "--git-dir")
	if err != nil || GitAnsweredNotARepository(result, err) {
		t.Fatalf("a successful probe is no such answer: %v", err)
	}
	if GitAnsweredNotARepository(outside, errors.New("not a git repository")) {
		t.Fatal("an error that is not a git exit is no such answer")
	}
	other, err := RunGitProbe(t.Context(), inside, maxOriginHeadBytes, "rev-parse", "--verify", "--end-of-options", "refs/heads/absent")
	if err == nil || GitAnsweredNotARepository(other, err) {
		t.Fatalf("a git exit with another message is no such answer: %q, %v", other.Stderr, err)
	}
}

// A .git file whose gitdir is missing is no repository on every git version, though git 2.56
// words the answer differently; a .git file that is not a gitfile at all is a malformed
// checkout, not that answer.
func TestGitAnsweredNotARepository_Gitfile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dangling := t.TempDir()
	writeGitfile(t, dangling, "gitdir: "+filepath.Join(dangling, "missing")+"\n")
	result, err := RunGitProbe(t.Context(), dangling, maxOriginHeadBytes, "rev-parse", "--git-dir")
	if !GitAnsweredNotARepository(result, err) {
		t.Fatalf("a gitfile naming a missing gitdir must be no repository: %q, %v", result.Stderr, err)
	}
	malformed := t.TempDir()
	writeGitfile(t, malformed, "not a gitfile\n")
	result, err = RunGitProbe(t.Context(), malformed, maxOriginHeadBytes, "rev-parse", "--git-dir")
	if err == nil || GitAnsweredNotARepository(result, err) {
		t.Fatalf("a malformed .git file is no such answer: %q, %v", result.Stderr, err)
	}
}

func writeGitfile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A target outside origin's branches, a missing directory and a context that already ended are
// errors: none of them answers which branch origin's HEAD names.
func TestReadOriginHeadBranch_Boundary_UnansweredReadsAreErrors(t *testing.T) {
	for _, target := range []string{"refs/heads/main", "refs/remotes/upstream/main", "refs/remotes/origin-mirror/main"} {
		dir := originHeadRepo(t, target)
		if got, ok, err := ReadOriginHeadBranch(t.Context(), dir); err == nil || ok || got != "" {
			t.Fatalf("origin HEAD at %s: got %q, %v, %v; want an error", target, got, ok, err)
		}
	}
	if _, ok, err := ReadOriginHeadBranch(t.Context(), filepath.Join(t.TempDir(), "absent")); err == nil || ok {
		t.Fatalf("a missing directory must be an error, got %v, %v", ok, err)
	}
	dir := originHeadRepo(t, originBranchPrefix+"main")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok, err := ReadOriginHeadBranch(ctx, dir); !errors.Is(err, context.Canceled) || ok {
		t.Fatalf("a cancelled read must be an error, got %v, %v", ok, err)
	}
}
