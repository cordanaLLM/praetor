// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package harvester

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// driftGit returns a runner for fixture git commands under one HermeticGitEnv, so no
// workstation configuration (signing, autocrlf, hooks) shapes the committed blobs.
func driftGit(t *testing.T) func(dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	return func(dir string, args ...string) string {
		t.Helper()
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := util.RunGit(runCtx, dir, args...)
		if err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
		}
		return out
	}
}

// driftRepo creates the repository root/name, writes files (slash paths) and commits them; an
// empty files map leaves the repository without a commit.
func driftRepo(t *testing.T, git func(string, ...string) string, root, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(dir, "init", "--quiet")
	if len(files) == 0 {
		return dir
	}
	for rel, content := range files {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(dir, "add", "--all")
	git(dir, "commit", "--quiet", "--no-verify", "-m", "fixture")
	return dir
}

// surveyDevRoot inventories root and surveys it, failing the test on an inventory error.
func surveyDevRoot(t *testing.T, root string, paths []string) (*DriftReport, error) {
	t.Helper()
	inventory, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	return SurveyDrift(t.Context(), inventory, DriftOptions{Root: root, Paths: paths})
}

func findSharedFile(files []SharedFile, name string) (SharedFile, bool) {
	for _, file := range files {
		if file.Path == name {
			return file, true
		}
	}
	return SharedFile{}, false
}

// Positive: a copy two repositories carry with different contents is drifted, with each
// variant's line count and its line delta from the first; a matching copy is identical; a path
// one repository carries alone is no finding. Repositories are named relative to the dev root
// in slash form and carry the HEAD commit read.
func TestSurveyDrift_Positive_ReportsDriftedAndIdenticalCopies(t *testing.T) {
	git := driftGit(t)
	root := t.TempDir()
	guard := "import sys\nBLOCK = ['/home/']\ndef main():\n    return 0\n"
	driftRepo(t, git, root, "alpha", map[string]string{
		"scripts/guard.py": guard, "scripts/same.sh": "echo same\n", ".githooks/pre-commit": "exit 0\n",
	})
	driftRepo(t, git, root, "org/beta", map[string]string{
		"scripts/guard.py": strings.Replace(guard, "['/home/']", "['/home/', '/Users/']", 1) + "# extra\n",
		"scripts/only.sh":  "echo alone\n",
	})
	driftRepo(t, git, root, "gamma", map[string]string{"scripts/same.sh": "echo same\n", "README": "not surveyed\n"})

	report, err := surveyDevRoot(t, root, nil)
	if err != nil || !report.Complete {
		t.Fatalf("survey = %+v, err %v", report, err)
	}
	drifted, ok := findSharedFile(report.Drifted, "scripts/guard.py")
	if !ok || len(report.Drifted) != 1 || len(drifted.Variants) != 2 {
		t.Fatalf("drifted = %+v, want scripts/guard.py with two variants", report.Drifted)
	}
	first, second := drifted.Variants[0], drifted.Variants[1]
	if !reflect.DeepEqual(first.Repositories, []string{"alpha"}) || !reflect.DeepEqual(second.Repositories, []string{"org/beta"}) {
		t.Fatalf("variant repositories = %v, %v", first.Repositories, second.Repositories)
	}
	if first.Lines != 4 || second.Lines != 5 || second.LinesRemoved != 1 || second.LinesAdded != 2 || first.LinesAdded != 0 {
		t.Fatalf("variant measures = %+v / %+v", first, second)
	}
	if !strings.HasPrefix(first.Digest, "sha256:") || first.Digest == second.Digest {
		t.Fatalf("digests = %q, %q", first.Digest, second.Digest)
	}
	same, ok := findSharedFile(report.Identical, "scripts/same.sh")
	if !ok || len(report.Identical) != 1 || !reflect.DeepEqual(same.Variants[0].Repositories, []string{"alpha", "gamma"}) {
		t.Fatalf("identical = %+v, want scripts/same.sh in alpha and gamma", report.Identical)
	}
	if len(report.Repositories) != 3 || report.Repositories[1].Name != "gamma" || report.Repositories[2].Name != "org/beta" {
		t.Fatalf("repositories = %+v", report.Repositories)
	}
	for _, repository := range report.Repositories {
		if repository.State != DriftRepositorySurveyed || len(repository.Revision) < 40 {
			t.Fatalf("repository = %+v, want surveyed at a commit", repository)
		}
	}
}

// Negative: a repository whose identity cannot be read and a copy above MaxDriftBlobBytes leave
// the report incomplete, naming the repository and the path, instead of passing as no drift.
func TestSurveyDrift_Negative_UnreadableRepositoryAndOversizedCopy(t *testing.T) {
	git := driftGit(t)
	root := t.TempDir()
	large := strings.Repeat("x", MaxDriftBlobBytes) + "\n"
	driftRepo(t, git, root, "alpha", map[string]string{"scripts/big.txt": large})
	driftRepo(t, git, root, "beta", map[string]string{"scripts/big.txt": large})
	if err := os.MkdirAll(filepath.Join(root, "broken", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := surveyDevRoot(t, root, nil)
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if report.Complete || len(report.Identical) != 0 || len(report.Drifted) != 0 {
		t.Fatalf("report = %+v, want incomplete with no comparison", report)
	}
	joined := strings.Join(report.Errors, "\n")
	for _, want := range []string{"broken: repository identity unknown", "alpha: scripts/big.txt unread", "exceed the"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("errors %q lack %q", joined, want)
		}
	}
}

// Negative: a path that is absolute, leaves the repository, names the whole tree, carries a
// drive letter or pathspec magic, or looks like an option is refused, as is an oversized list.
func TestNormalizeDriftPaths_Negative_RefusesUnsafePaths(t *testing.T) {
	for _, raw := range []string{"", " ", "/etc", `\\server\share`, "..", "../x", "a/../../x", ".", "./", `C:\x`, ":(glob)x", "-x"} {
		if got, err := NormalizeDriftPaths([]string{raw}); err == nil {
			t.Errorf("NormalizeDriftPaths(%q) = %v, want an error", raw, got)
		}
	}
	if _, err := NormalizeDriftPaths(make([]string, MaxDriftPaths+1)); err == nil {
		t.Fatal("more than MaxDriftPaths paths were accepted")
	}
	if _, err := SurveyDrift(t.Context(), &WorkstationReport{}, DriftOptions{Paths: []string{"/abs"}}); err == nil {
		t.Fatal("SurveyDrift accepted an absolute path")
	}
	if _, err := SurveyDrift(t.Context(), nil, DriftOptions{}); err == nil {
		t.Fatal("SurveyDrift accepted a missing inventory")
	}
}

// Positive and boundary: backslashes read as separators, trailing slashes stay, duplicates
// collapse, the result is sorted, and an empty list selects the defaults.
func TestNormalizeDriftPaths_Boundary_NormalizesAndDefaults(t *testing.T) {
	got, err := NormalizeDriftPaths([]string{`tools\hooks\`, "scripts/", "scripts/", "b/./c.sh"})
	if err != nil || !reflect.DeepEqual(got, []string{"b/c.sh", "scripts/", "tools/hooks/"}) {
		t.Fatalf("normalized = %v, err %v", got, err)
	}
	defaults, err := NormalizeDriftPaths(nil)
	if err != nil || !reflect.DeepEqual(defaults, DefaultDriftPaths()) {
		t.Fatalf("defaults = %v, err %v", defaults, err)
	}
	defaults[0] = "mutated"
	if DefaultDriftPaths()[0] == "mutated" {
		t.Fatal("DefaultDriftPaths shares its backing array with callers")
	}
}

// Boundary: a linked worktree of a surveyed repository is not a second copy, a repository
// without a commit is reported unborn without failing the survey, a prefix does not match a
// sibling that merely starts with the same letters, and a committed symbolic link is no copy.
func TestSurveyDrift_Boundary_WorktreesUnbornPrefixesAndLinks(t *testing.T) {
	git := driftGit(t)
	root := t.TempDir()
	alpha := driftRepo(t, git, root, "alpha", map[string]string{"scripts/a.sh": "a\n", "scriptsx/b.sh": "b\n"})
	git(alpha, "worktree", "add", "--quiet", "-b", "wt", filepath.Join(root, "alpha-worktrees", "wt"))
	beta := driftRepo(t, git, root, "beta", map[string]string{"scriptsx/b.sh": "b2\n", "target.txt": "t\n"})
	blob := git(beta, "hash-object", "-w", "target.txt")
	git(beta, "update-index", "--add", "--cacheinfo", "120000,"+blob+",scripts/a.sh")
	git(beta, "commit", "--quiet", "--no-verify", "-m", "link")
	driftRepo(t, git, root, "empty", nil)

	inventory, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	worktrees := 0
	for _, observation := range inventory.RepositoryObservations {
		if observation.Classification == "linked-worktree" {
			worktrees++
		}
	}
	if worktrees != 1 {
		t.Fatalf("inventory holds %d linked worktrees, want the fixture's one: %+v", worktrees, inventory.RepositoryObservations)
	}
	report, err := SurveyDrift(t.Context(), inventory, DriftOptions{Root: root, Paths: []string{"scripts"}})
	if err != nil || !report.Complete {
		t.Fatalf("survey = %+v, err %v", report, err)
	}
	if len(report.Drifted) != 0 || len(report.Identical) != 0 {
		t.Fatalf("findings = %+v / %+v, want none", report.Drifted, report.Identical)
	}
	names := make([]string, 0, len(report.Repositories))
	for _, repository := range report.Repositories {
		names = append(names, repository.Name+"="+repository.State)
	}
	want := []string{"alpha=surveyed", "beta=surveyed", "empty=unborn"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("repositories = %v, want %v", names, want)
	}
}

// Boundary: a truncated inventory and a cancelled survey both leave the report incomplete, and
// the cancellation is returned as an error with the partial report.
func TestSurveyDrift_Boundary_TruncatedInventoryAndCancellation(t *testing.T) {
	report, err := SurveyDrift(t.Context(), &WorkstationReport{RepositoryInventoryTruncated: true}, DriftOptions{})
	if err != nil || report.Complete || !report.Truncated || report.InventoryComplete {
		t.Fatalf("truncated inventory = %+v, err %v", report, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	inventory := &WorkstationReport{RepositoryInventoryComplete: true}
	report, err = SurveyDrift(ctx, inventory, DriftOptions{})
	if err == nil || report == nil || report.Complete {
		t.Fatalf("cancelled survey = %+v, err %v", report, err)
	}
}
