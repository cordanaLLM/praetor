// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
