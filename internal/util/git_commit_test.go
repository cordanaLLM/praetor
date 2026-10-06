// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"strings"
	"testing"
)

// committedRepo is originHeadRepo with one commit on HEAD, which refs/remotes/origin/main names
// too, and returns the commit's full name.
func committedRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := originHeadRepo(t, "")
	identity := []string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	if _, err := RunGit(t.Context(), dir, append(identity, "commit", "-q", "--allow-empty", "-m", "fixture")...); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	head, err := RunGit(t.Context(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	head = strings.TrimSpace(head)
	if _, err := RunGit(t.Context(), dir, "update-ref", originBranchPrefix+"main", head); err != nil {
		t.Fatalf("git update-ref: %v", err)
	}
	return dir, head
}

// Positive: a branch, a remote-tracking ref and an abbreviated object name resolve to the full
// name of the commit they name.
func TestResolveGitCommit_Positive_NamesTheCommit(t *testing.T) {
	dir, head := committedRepo(t)
	for _, name := range []string{"HEAD", originBranchPrefix + "main", head[:12]} {
		got, err := ResolveGitCommit(t.Context(), dir, name)
		if err != nil || got != head {
			t.Fatalf("%s: got %q, %v; want %s", name, got, err, head)
		}
	}
}

// Negative: a ref the checkout does not hold and an object that is no commit answer "none", not
// an error, so a caller can tell an absent ref from a read that failed.
func TestResolveGitCommit_Negative_AbsentCommitIsNone(t *testing.T) {
	dir, _ := committedRepo(t)
	tree, err := RunGit(t.Context(), dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	for _, name := range []string{originBranchPrefix + "release", strings.TrimSpace(tree)} {
		got, err := ResolveGitCommit(t.Context(), dir, name)
		if err != nil || got != "" {
			t.Fatalf("%s: got %q, %v; want none", name, got, err)
		}
	}
}

// Boundary: a name that would reach git as an option, a directory outside any checkout and a
// cancelled context are errors, never "none".
func TestResolveGitCommit_Boundary_UnansweredIsAnError(t *testing.T) {
	dir, _ := committedRepo(t)
	if got, err := ResolveGitCommit(t.Context(), dir, "--all"); err == nil || got != "" {
		t.Fatalf("an option-shaped name: got %q, %v; want an error", got, err)
	}
	if got, err := ResolveGitCommit(t.Context(), t.TempDir(), "HEAD"); err == nil || got != "" {
		t.Fatalf("a directory outside any checkout: got %q, %v; want an error", got, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := ResolveGitCommit(cancelled, dir, "HEAD"); err == nil || got != "" {
		t.Fatalf("a cancelled read: got %q, %v; want an error", got, err)
	}
}
