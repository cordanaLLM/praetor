// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Order finding-engine-1730-landing-whitespace-20261031: patch-package files under
// console/patches carry upstream bytes as diff context, trailing spaces and
// space-before-tab indents included. The repository's .gitattributes keeps whitespace
// checks off those files only, so `git diff --check` (and the commit hook re-run in CI)
// still refuses the same bytes everywhere else.

// patchWhitespaceBody holds both whitespace errors git reports: a trailing space and a
// space before a tab in the indent.
const patchWhitespaceBody = "context line with a trailing space \n \tspace before tab\n"

// diffCheckWithRepositoryAttributes commits rel with patchWhitespaceBody in a fixture
// repository that carries this repository's .gitattributes, then runs git diff --check
// over that commit.
func diffCheckWithRepositoryAttributes(t *testing.T, rel string) (string, error) {
	t.Helper()
	attrs, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatalf("read repository .gitattributes: %v", err)
	}
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".gitattributes", string(attrs))
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, rel, patchWhitespaceBody)
	gitCommitAll(t, dir, env, "add "+rel)
	return runFixtureGit(t, dir, env, "diff", "--check", "HEAD~1", "HEAD")
}

// Positive: a patch-package file with upstream whitespace passes git diff --check.
func TestPatchWhitespaceAttr_Positive_PatchPackageFilePasses(t *testing.T) {
	out, err := diffCheckWithRepositoryAttributes(t, "console/patches/pkg@1.0.0.patch")
	if err != nil {
		t.Fatalf("git diff --check refused a console/patches file: %v\n%s", err, out)
	}
}

// Negative: the same bytes in a console source file are still refused, naming the file.
func TestPatchWhitespaceAttr_Negative_SourceFileStillRefused(t *testing.T) {
	out, err := diffCheckWithRepositoryAttributes(t, "console/src/lib/planted.ts")
	if err == nil {
		t.Fatal("git diff --check passed trailing whitespace in console/src/lib/planted.ts")
	}
	if !strings.Contains(out, "console/src/lib/planted.ts") {
		t.Fatalf("git diff --check output does not name the planted file:\n%s", out)
	}
}

// Boundary: a .patch file outside console/patches is not exempt.
func TestPatchWhitespaceAttr_Boundary_PatchOutsideConsolePatchesRefused(t *testing.T) {
	out, err := diffCheckWithRepositoryAttributes(t, "docs/planted.patch")
	if err == nil {
		t.Fatal("git diff --check passed trailing whitespace in docs/planted.patch")
	}
	if !strings.Contains(out, "docs/planted.patch") {
		t.Fatalf("git diff --check output does not name the planted file:\n%s", out)
	}
}
