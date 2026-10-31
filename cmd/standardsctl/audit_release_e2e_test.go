// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func initAuditE2ERepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForAuditTest(t, dir, "init")
	runGitForAuditTest(t, dir, "config", "user.name", "Test User")
	runGitForAuditTest(t, dir, "config", "user.email", "test@example.com")
	runGitForAuditTest(t, dir, "config", "commit.gpgsign", "false")
	runGitForAuditTest(t, dir, "config", "tag.gpgsign", "false")
	return dir
}

func runGitForAuditTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeAndCommitAuditTestFile(t *testing.T, dir, rel, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForAuditTest(t, dir, "add", rel)
	runGitForAuditTest(t, dir, "commit", "-m", msg)
}

func TestAuditReleaseE2E_FlagAndGate(t *testing.T) {
	opts, err := parseAuditOptions([]string{"--release"})
	if err != nil || !opts.release {
		t.Fatalf("parseAuditOptions(--release) = %+v, %v", opts, err)
	}

	repo := initAuditE2ERepo(t)
	writeAndCommitAuditTestFile(t, repo, "src/a.go", "package src\n", "chore: init")
	runGitForAuditTest(t, repo, "tag", "v0.1.0")
	writeAndCommitAuditTestFile(t, repo, "src/b.go", "package src\n", "feat: add b")

	emptyManifest := &config.Manifest{}
	out, err := captureStdout(t, func() error {
		return auditReleaseE2E(t.Context(), emptyManifest, repo, true)
	})
	if err != nil || strings.TrimSpace(out) != "[SKIP] Release end-to-end evidence not checked: release.e2e not declared." {
		t.Fatalf("missing section auditReleaseE2E = %q, %v", out, err)
	}

	manifest := &config.Manifest{Release: &config.ReleasePolicy{E2E: &config.ReleaseE2EPolicy{}}}
	if _, err := captureStdout(t, func() error {
		return auditReleaseE2E(t.Context(), manifest, repo, false)
	}); err != nil {
		t.Fatalf("non-release auditReleaseE2E expected pass with warning, got %v", err)
	}
	err = auditReleaseE2E(t.Context(), manifest, repo, true)
	if err == nil || !strings.Contains(err.Error(), "[FAIL] Release end-to-end evidence:") {
		t.Fatalf("release auditReleaseE2E expected [FAIL], got %v", err)
	}
}

func TestAuditReleaseTestLevels_GateAndAuditWiring(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "go.mod", "module example.com/lib\n\ngo 1.25.0\n")
	writeFixtureFile(t, f.dir, "pkg/lib.go", "// Package lib provides a helper.\npackage lib\n\n// Value returns one.\nfunc Value() int { return 1 }\n")
	writeFixtureFile(t, f.dir, "pkg/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {}\n")
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml",
		"name: CI\non:\n  pull_request:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    timeout-minutes: 10\n    steps:\n      - run: go test -vet=all ./...\n")
	writeDeclaredRuleset(t, f.dir, config.DefaultPolicy().BranchProtection)

	manifest := &config.Manifest{Profiles: []string{"framework"}}
	// Non-release context warns without failing.
	warnOut, warnErr := captureStdout(t, func() error {
		return auditReleaseTestLevels(t.Context(), manifest, f.dir, false)
	})
	if warnErr != nil || !strings.Contains(warnOut, "[WARN] Release test levels:") {
		t.Fatalf("expected [WARN] outside release context, got out=%q err=%v", warnOut, warnErr)
	}

	// Full audit with --release fails on the unbaselined integration gap.
	_, relErr := f.audit(t, "--release")
	if relErr == nil || !strings.Contains(relErr.Error(), "[FAIL] Release test levels:") {
		t.Fatalf("expected full audit --release to fail on missing integration level, got %v", relErr)
	}

	// Recording a baseline when the working baseline carries a RELEASE-TEST-LEVEL anchor absent
	// from the default branch is refused even with --allow-increase.
	infs := []baseline.Infraction{{
		RuleID:     baseline.ReleaseTestLevelRuleID,
		FilePath:   ".standards.yaml",
		LineNumber: 1,
		Anchor:     "level:library-client:integration",
		Message:    "added release test level gap",
	}}
	baseline.AssignFingerprints(infs)
	baseSnap := &baseline.Baseline{Version: 1, Repository: "acme/lib", Infractions: infs, TotalInfractions: 1}
	if err := baseline.SaveBaseline(filepath.Join(f.dir, ".standards-baseline.json"), baseSnap); err != nil {
		t.Fatal(err)
	}
	_, recErr := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=attempt to add test level anchor")
	if recErr == nil || !strings.Contains(recErr.Error(), "shrink-only") {
		t.Fatalf("expected baseline --record to refuse added RELEASE-TEST-LEVEL anchor, got %v", recErr)
	}
}
