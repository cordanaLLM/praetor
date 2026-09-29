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
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// gitSandbox isolates git from the host configuration and returns an empty directory.
func gitSandbox(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runRepoGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(envCtx, 30*time.Second)
	defer cancel()
	if out, err := util.RunGit(ctx, dir, args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ambiguousBaseRepo builds a repository whose base ref "base" names both a branch and a tag,
// so every diff against it makes git print "warning: refname 'base' is ambiguous." on
// standard error while exiting zero. HEAD adds docs/new.md on top of base.
func ambiguousBaseRepo(t *testing.T) string {
	t.Helper()
	dir := gitSandbox(t)
	writeRepoFile(t, dir, "README.md", "README.md\n")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "README.md"}, {"commit", "-q", "-m", "base"},
		{"branch", "base"}, {"tag", "base"},
	} {
		runRepoGit(t, dir, args...)
	}
	writeRepoFile(t, dir, "docs/new.md", "docs/new.md\n")
	runRepoGit(t, dir, "add", "docs/new.md")
	runRepoGit(t, dir, "commit", "-q", "-m", "head")
	return dir
}

// docsBranchRepo builds a repository whose main branch holds code and whose checked-out
// docs branch commits one README.md edit on top, so the targeted decision is docs-only.
func docsBranchRepo(t *testing.T) string {
	t.Helper()
	dir := gitSandbox(t)
	writeRepoFile(t, dir, "main.go", "package main\n")
	writeRepoFile(t, dir, "README.md", "# Fixture\n")
	runRepoGit(t, dir, "init", "-q", "-b", "main")
	runRepoGit(t, dir, "add", ".")
	runRepoGit(t, dir, "commit", "-q", "-m", "base")
	runRepoGit(t, dir, "checkout", "-q", "-b", "docs")
	writeRepoFile(t, dir, "README.md", "# Fixture\n\nMore docs.\n")
	runRepoGit(t, dir, "commit", "-q", "-am", "docs only")
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

// Negative (BUG-891): an unresolvable base ref used to fall back to `git diff HEAD`, the
// uncommitted working tree. A dirty README.md then made a branch of committed code changes
// read as docs-only and skip the heavy gates. The ref failure must surface instead, and
// AnalyzeChanges must turn it into the full matrix.
func TestUnresolvableBaseRefFailsClosedOnADirtyTree(t *testing.T) {
	dir := docsBranchRepo(t)
	writeRepoFile(t, dir, "README.md", "# Fixture\n\nUncommitted edit.\n")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if files, err := cifilter.GetChangedFiles(ctx, dir, "no-such-ref", "HEAD"); err == nil {
		t.Fatalf("unresolvable base ref must fail, got files %q", files)
	}
	decision, err := cifilter.AnalyzeChanges(ctx, cifilter.FilterOptions{RepoDir: dir, BaseRef: "no-such-ref"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.RunDocsOnly || decision.SkipHeavyGates || !decision.RunTests || !decision.RunSecurity ||
		!strings.Contains(decision.Reason, "git diff unavailable") {
		t.Fatalf("unresolvable base ref must select the full matrix, got %+v", decision)
	}
}

// overrides.ci reaches the decision through FilterOptions.ManifestPath (BUG-652). The same
// docs-only branch is analysed under each manifest shape.
func TestAnalyzeChangesHonoursManifestCIPolicy(t *testing.T) {
	dir := docsBranchRepo(t)
	manifest := filepath.Join(t.TempDir(), ".standards.yaml")
	analyze := func() *cifilter.FilterDecision {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		decision, err := cifilter.AnalyzeChanges(ctx, cifilter.FilterOptions{
			RepoDir: dir, BaseRef: "main", HeadRef: "HEAD", ManifestPath: manifest,
		})
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}

	// Boundary: no manifest file keeps the default docs-only decision.
	if d := analyze(); !d.RunDocsOnly || !d.SkipHeavyGates || d.RunTests {
		t.Fatalf("absent manifest must keep the docs-only decision, got %+v", d)
	}
	// Positive: the manifest switches each force the full matrix on a docs-only change.
	for key, other := range map[string]string{
		"diff_aware_filtering":              "skip_heavy_gates_on_docs_or_state",
		"skip_heavy_gates_on_docs_or_state": "diff_aware_filtering",
	} {
		writeRepoFile(t, filepath.Dir(manifest), ".standards.yaml",
			"version: 1\noverrides:\n  ci:\n    "+key+": false\n    "+other+": true\n")
		d := analyze()
		if d.RunDocsOnly || d.SkipHeavyGates || !d.RunTests || !d.RunSecurity || !strings.Contains(d.Reason, key) {
			t.Fatalf("%s=false must select the full matrix, got %+v", key, d)
		}
	}
	// Negative: a manifest that does not parse fails closed to the full matrix.
	writeRepoFile(t, filepath.Dir(manifest), ".standards.yaml", "version: 1\noverides: {}\n")
	if d := analyze(); d.SkipHeavyGates || !d.RunTests || !strings.Contains(d.Reason, "CI policy unavailable") {
		t.Fatalf("an unparsable manifest must select the full matrix, got %+v", d)
	}
}
