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

// No checkout and a checkout that never recorded origin HEAD both answer "none", not an error.
func TestReadOriginHeadBranch_Negative_NoneRecordedIsNotAnError(t *testing.T) {
	if got, ok, err := ReadOriginHeadBranch(t.Context(), originHeadRepo(t, "")); err != nil || ok || got != "" {
		t.Fatalf("a checkout without origin HEAD: got %q, %v, %v", got, ok, err)
	}
	if got, ok, err := ReadOriginHeadBranch(t.Context(), t.TempDir()); err != nil || ok || got != "" {
		t.Fatalf("a directory outside any checkout: got %q, %v, %v", got, ok, err)
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
