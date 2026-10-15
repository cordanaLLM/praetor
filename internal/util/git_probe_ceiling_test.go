// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #828: a probe honours an inherited GIT_CEILING_DIRECTORIES. Negative: from a scratch
// directory nested in a repository, with the ceiling at that repository's root, the probe
// answers "not a git repository". Positive: without the ceiling it finds the repository.
func TestRunGitProbe_Negative_HonoursAnInheritedCeiling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git required: %v", err)
	}
	outer := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", outer).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	scratch := filepath.Join(outer, "scratch")
	if err := os.Mkdir(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", outer)
	result, err := RunGitProbe(t.Context(), scratch, 4096, "rev-parse", "--git-dir")
	if !GitAnsweredNotARepository(result, err) {
		t.Fatalf("probe below the ceiling: err %v, stdout %q, stderr %q; want not a git repository",
			err, result.Stdout, result.Stderr)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", "")
	result, err = RunGitProbe(t.Context(), scratch, 4096, "rev-parse", "--git-dir")
	if err != nil || !bytes.Contains(result.Stdout, []byte(".git")) {
		t.Fatalf("probe without a ceiling: err %v, stdout %q; want the enclosing repository", err, result.Stdout)
	}
}

// boundGitDiscovery is testsupport.BoundGitDiscovery for this package's own tests, which
// testsupport cannot serve because it imports util: the process ceiling at the temporary
// directory every test's scratch directories sit in, subtests' included, so in-process git
// never climbs into an enclosing checkout.
func boundGitDiscovery(t *testing.T) {
	t.Helper()
	ceiling, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
}

// #828: GitWorktreePresent answers as git does under a ceiling. Negative: a scratch directory
// nested in a repository whose root is a ceiling has no work tree. Positive: without the
// ceiling the enclosing repository is found.
func TestGitWorktreePresent_Negative_StopsAtTheCeiling(t *testing.T) {
	outer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outer, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outer, ".git", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(outer, "scratch")
	if err := os.Mkdir(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", outer)
	if present, err := GitWorktreePresent(t.Context(), scratch); err != nil || present {
		t.Fatalf("below the ceiling: present=%v err=%v; want no work tree", present, err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", "")
	if present, err := GitWorktreePresent(t.Context(), scratch); err != nil || !present {
		t.Fatalf("without a ceiling: present=%v err=%v; want the enclosing repository", present, err)
	}
}
