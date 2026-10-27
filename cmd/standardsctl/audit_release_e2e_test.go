// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	manifest := &config.Manifest{}
	if err := auditReleaseE2E(t.Context(), manifest, repo, false); err != nil {
		t.Fatalf("non-release auditReleaseE2E expected pass with warning, got %v", err)
	}
	err = auditReleaseE2E(t.Context(), manifest, repo, true)
	if err == nil || !strings.Contains(err.Error(), "[FAIL] Release end-to-end evidence:") {
		t.Fatalf("release auditReleaseE2E expected [FAIL], got %v", err)
	}
}
