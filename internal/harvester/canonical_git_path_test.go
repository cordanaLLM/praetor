// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package harvester

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// canonicalGitPath answers with one spelling whichever of git's two answer shapes it is
// given, and refuses rather than falling back when the path cannot be canonicalised.
func TestCanonicalGitPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(real, "repo", ".git"))
	if err != nil {
		t.Fatal(err)
	}
	// Positive: the relative answer a main checkout receives and the absolute answer a
	// linked worktree receives canonicalise to one string, although one is spelled through
	// the alias and the other is not.
	for _, tc := range []struct{ name, repoPath, value string }{
		{"relative answer under an aliased root", filepath.Join(alias, "repo"), ".git"},
		{"absolute answer from git", filepath.Join(alias, "repo"), filepath.Join(real, "repo", ".git")},
		{"absolute answer spelled through the alias", real, filepath.Join(alias, "repo", ".git")},
		{"uncleaned relative answer", filepath.Join(alias, "repo"), filepath.Join("..", "repo", ".git")},
	} {
		got, err := canonicalGitPath(t.Context(), tc.repoPath, tc.value)
		if err != nil || got != want {
			t.Errorf("%s: canonicalGitPath = %q %v, want %q", tc.name, got, err, want)
		}
	}
	// Negative: a path that cannot be resolved is refused, never answered with the spelling
	// that was handed in.
	if err := os.Symlink("cycle", filepath.Join(root, "cycle")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if got, err := canonicalGitPath(t.Context(), root, "cycle"); err == nil {
		t.Errorf("a symlink cycle was canonicalised to %q", got)
	}
	// Boundary: an unusable context is an error rather than an unbounded resolution.
	var absent context.Context
	if got, err := canonicalGitPath(absent, real, ".git"); err == nil {
		t.Errorf("a missing context was canonicalised to %q", got)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := canonicalGitPath(cancelled, real, ".git"); err == nil {
		t.Errorf("a cancelled context was canonicalised to %q", got)
	}
}
