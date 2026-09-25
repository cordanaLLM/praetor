// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cifilter_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ambiguousBaseRepo builds a repository whose base ref "base" names both a branch and a tag,
// so every diff against it makes git print "warning: refname 'base' is ambiguous." on
// standard error while exiting zero. HEAD adds docs/new.md on top of base.
func ambiguousBaseRepo(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "praetor-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "praetor-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	dir := t.TempDir()
	write := func(name string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	steps := [][]string{
		{"init", "-q", "-b", "main"}, {"add", "README.md"}, {"commit", "-q", "-m", "base"},
		{"branch", "base"}, {"tag", "base"}, {"add", "docs/new.md"}, {"commit", "-q", "-m", "head"},
	}
	write("README.md")
	for i, args := range steps {
		if i == 5 {
			write("docs/new.md")
		}
		if out, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// A git warning on standard error is not a changed file. Parsed as one, it made the CI
// filter and the audit ratchet classify a path that does not exist (BUG-847).
func TestGetChangedFiles_IgnoresWarningsOnStandardError(t *testing.T) {
	dir := ambiguousBaseRepo(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	files, err := cifilter.GetChangedFiles(ctx, dir, "base", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, "\n") != "docs/new.md" {
		t.Fatalf("changed files = %q, want only docs/new.md", files)
	}
}
