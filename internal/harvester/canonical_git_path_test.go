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
	gitfile := filepath.Join(real, "repo", "gitfile")
	if err := os.WriteFile(gitfile, []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, repoPath, value string }{
		{"a symlink cycle", root, "cycle"},
		// git answers --git-dir and --git-common-dir with a directory, so a regular file at
		// the answered path is a repository this probe must not classify from. The check is
		// util.DirExists, which refuses it; a bare existence test would accept it.
		{"an answer naming a regular file", filepath.Join(real, "repo"), "gitfile"},
		// util.ResolveExistingPath answers a path that is not there with the deepest
		// existing ancestor resolved and the missing tail re-attached, and no error --
		// which is what callers creating a path need and exactly the half-resolved
		// spelling this function must not publish as a git directory.
		{"a relative answer naming a directory that is not there", real, "absent"},
		{"an absolute answer naming a directory that is not there", real, filepath.Join(real, "absent", ".git")},
		{"a nested path whose parent is not there", real, filepath.Join("absent", "worktrees", "one")},
	} {
		if got, err := canonicalGitPath(t.Context(), tc.repoPath, tc.value); err == nil {
			t.Errorf("%s was canonicalised to %q", tc.name, got)
		}
	}
	// Boundary: an unusable context is an error rather than an unbounded resolution. Both rows
	// name the repository that exists, real/repo, so the existence check at harvester.go:479
	// cannot answer for them: the context guard in util.ResolveExistingPath is the only thing
	// left that can refuse. Pointed at real, as they were, real/.git does not exist and the
	// rows passed with the guards removed -- measured by stripping both ctx.Err() checks from
	// internal/util/resolved_path.go, which left this test green while internal/util's own
	// TestResolveExistingPath failed.
	repo := filepath.Join(real, "repo")
	var absent context.Context
	if got, err := canonicalGitPath(absent, repo, ".git"); err == nil {
		t.Errorf("a missing context was canonicalised to %q", got)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := canonicalGitPath(cancelled, repo, ".git"); err == nil {
		t.Errorf("a cancelled context was canonicalised to %q", got)
	}
	// The same call with a usable context is the control: without it the two rows above could
	// be refused for any reason and still read as a context guard.
	if got, err := canonicalGitPath(t.Context(), repo, ".git"); err != nil || got != want {
		t.Errorf("the same input with a usable context: %q %v, want %q", got, err, want)
	}
}
