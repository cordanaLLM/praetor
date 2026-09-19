// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"os"
	"path/filepath"
	"testing"
)

// aliasedTree builds base/real/repo and a base/alias symlink to base/real, so one directory
// is reachable under two spellings on every platform. It returns the real spelling, the
// aliased one and the base directory.
//
// An ancestor symlink is the portable stand-in for the two conditions that failed the
// Platform Neutrality matrix: macOS reaching its own TMPDIR through /var -> /private/var,
// and Windows handing out an 8.3 short name where git answers with the long one. Neither
// of those is reproducible on Linux; both are "one directory, two spellings", which is.
func aliasedTree(t *testing.T) (real, alias, base string) {
	t.Helper()
	base = t.TempDir()
	real = filepath.Join(base, "real", "repo")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "alias")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	return real, filepath.Join(base, "alias", "repo"), base
}

// Positive: identity, not string equality, decides. Two spellings of one directory match.
func TestSameDirectoryAcceptsAliasedSpellings(t *testing.T) {
	real, alias, _ := aliasedTree(t)
	if real == alias {
		t.Fatalf("fixture did not produce two spellings: %q", real)
	}
	// filepath.Join cleans, so an uncleaned spelling has to be assembled by hand: Join(real,
	// "..", "repo") is byte-identical to real and the row was a second copy of the one above
	// it. The concatenated form is the input a caller that never cleaned its own path hands
	// in, and the row refuses an implementation that rejects a "..", or compares cleaned
	// strings, rather than asking the filesystem.
	uncleaned := real + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(real)
	if filepath.Clean(uncleaned) != real || uncleaned == real {
		t.Fatalf("fixture did not produce an uncleaned spelling: %q", uncleaned)
	}
	for _, tc := range []struct{ name, left, right string }{
		{"aliased ancestor against the real path", alias, real},
		{"the same comparison the other way round", real, alias},
		{"a path against itself", real, real},
		{"an uncleaned spelling against the aliased one", uncleaned, alias},
		{"an uncleaned spelling against the real one", uncleaned, real},
	} {
		if !SameDirectory(tc.left, tc.right) {
			t.Errorf("%s: SameDirectory(%q, %q) = false, want true", tc.name, tc.left, tc.right)
		}
	}
}

// Negative: the refusals callers depend on. internal/adopt.ResolveGitHooksDir turns a false
// here into ErrHooksDirEscapesRepo, so every one of these must answer false rather than the
// "fall back to a string compare" behaviour of the internal/adopt.samePath it replaced --
// that one could report an unstattable path as a match.
func TestSameDirectoryRefusesDistinctAndUnresolvablePaths(t *testing.T) {
	real, _, base := aliasedTree(t)
	sibling := filepath.Join(base, "real", "other")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "real", "absent")
	if err := os.Symlink(filepath.Join(base, "cycle"), filepath.Join(base, "cycle")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	for _, tc := range []struct{ name, left, right string }{
		{"two different existing directories", real, sibling},
		{"a directory that does not exist, on the right", real, missing},
		{"a directory that does not exist, on the left", missing, real},
		{"two spellings of one path that does not exist", missing, filepath.Join(base, "real", "absent")},
		{"a symlink that cannot be resolved", filepath.Join(base, "cycle"), real},
		{"a symlink to a path that does not exist", dangling, real},
		{"a dangling symlink against itself", dangling, dangling},
	} {
		if SameDirectory(tc.left, tc.right) {
			t.Errorf("%s: SameDirectory(%q, %q) = true, want false", tc.name, tc.left, tc.right)
		}
	}
}

// Boundary: the empty spelling and the file/directory edge.
//
// The empty string is the one input os.Stat would answer for with an error anyway; it is
// rejected before the syscall so that a caller holding an unset path never reaches the
// filesystem. A file and the directory holding it are different inodes, so they do not
// match -- internal/harvester leans on exactly that to tell a linked worktree, whose .git
// is a file, from a main checkout, whose .git is a directory.
func TestSameDirectoryBoundaries(t *testing.T) {
	real, alias, _ := aliasedTree(t)
	file := filepath.Join(real, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, left, right string
		want              bool
	}{
		{"an empty left spelling", "", real, false},
		{"an empty right spelling", real, "", false},
		{"two empty spellings", "", "", false},
		{"a file against the directory holding it", file, real, false},
		{"a directory against a file inside it", real, file, false},
		// Identity is the whole criterion, so two spellings of one regular file match too.
		// Callers only ever ask about directories, which is what the name says.
		{"one file under two spellings", file, filepath.Join(alias, "file.txt"), true},
	} {
		if got := SameDirectory(tc.left, tc.right); got != tc.want {
			t.Errorf("%s: SameDirectory(%q, %q) = %v, want %v", tc.name, tc.left, tc.right, got, tc.want)
		}
	}
}
