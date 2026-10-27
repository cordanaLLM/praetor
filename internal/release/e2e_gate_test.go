// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.name", "Test User")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")
	runGitCmd(t, dir, "config", "tag.gpgsign", "false")
	return dir
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := util.RunGit(t.Context(), dir, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func commitFile(t *testing.T, dir, relPath, content, message string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	runGitCmd(t, dir, "add", relPath)
	runGitCmd(t, dir, "commit", "-m", message)
	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

func assertContainsAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for i := 0; i < len(wants) && i < 16; i++ {
		if !strings.Contains(text, wants[i]) {
			t.Fatalf("output %q missing %q", text, wants[i])
		}
	}
}

func TestAuditE2E_PlantedFeatTouchingSrcOnly(t *testing.T) {
	repo := initTestGitRepo(t)
	commitFile(t, repo, "src/init.go", "package src\n", "chore: initial commit")
	runGitCmd(t, repo, "tag", "v0.1.0")
	featSHA := commitFile(t, repo, "src/x.go", "package src\nfunc X() {}\n", "feat: add x")
	sha12 := featSHA[:12]

	// In release context: must FAIL naming sha12 and subject.
	findings, judgeErr := judge(t.Context(), repo, SettingsFromPolicy(nil), true)
	if judgeErr != nil || len(findings) != 1 || findings[0].SHA12() != sha12 {
		t.Fatalf("judge findings = %+v, err = %v", findings, judgeErr)
	}
	out, err := AuditE2E(t.Context(), repo, nil, true)
	if err == nil {
		t.Fatalf("release context expected FAIL error, got nil (out=%q)", out)
	}
	assertContainsAll(t, out, "[FAIL]", sha12, "feat: add x")

	// Outside release context: must WARN naming sha12 and subject, and audit passes (err == nil).
	warnOut, warnErr := AuditE2E(t.Context(), repo, nil, false)
	if warnErr != nil {
		t.Fatalf("non-release context expected nil error, got %v (out=%q)", warnErr, warnOut)
	}
	assertContainsAll(t, warnOut, "[WARN]", sha12, "feat: add x")
}

func TestAuditE2E_PathAndTrailerEvidence(t *testing.T) {
	repo := initTestGitRepo(t)
	commitFile(t, repo, "src/init.go", "package src\n", "chore: initial commit")
	runGitCmd(t, repo, "tag", "v0.1.0")

	// 1. feat commit touching tests/e2e/x_test.go -> PASS.
	commitFile(t, repo, "tests/e2e/x_test.go", "package e2e\n", "feat: add x with e2e test")
	out, err := AuditE2E(t.Context(), repo, nil, true)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected PASS for tests/e2e/x_test.go, got out=%q err=%v", out, err)
	}

	// 2. trailer E2E-Test: login-flow with tests: [login-flow] -> PASS.
	commitFile(t, repo, "src/login.go", "package src\n", "feat: add login\n\nE2E-Test: login-flow")
	policy := &config.ReleaseE2EPolicy{Tests: []string{"login-flow"}}
	out, err = AuditE2E(t.Context(), repo, policy, true)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected PASS for declared E2E-Test trailer, got out=%q err=%v", out, err)
	}

	// 3. trailer with undeclared id -> FAIL naming id.
	badSHA := commitFile(t, repo, "src/logout.go", "package src\n", "feat: add logout\n\nE2E-Test: undeclared-flow")
	out, err = AuditE2E(t.Context(), repo, policy, true)
	if err == nil || !strings.Contains(out, "undeclared-flow") || !strings.Contains(out, badSHA[:12]) {
		t.Fatalf("expected FAIL naming undeclared-flow and %s, got out=%q err=%v", badSHA[:12], out, err)
	}
}

func TestAuditE2E_FixScopeBangAndIgnoredTypes(t *testing.T) {
	repo := initTestGitRepo(t)
	commitFile(t, repo, "src/init.go", "package src\n", "chore: initial commit")
	runGitCmd(t, repo, "tag", "v0.1.0")

	// chore, docs, refactor commits -> ignored.
	commitFile(t, repo, "src/chore.go", "package src\n", "chore: tidy deps")
	commitFile(t, repo, "docs/readme.md", "# docs\n", "docs: update guide")
	commitFile(t, repo, "src/refactor.go", "package src\n", "refactor(core): simplify loop")
	out, err := AuditE2E(t.Context(), repo, nil, true)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected PASS when only chore/docs/refactor commits exist, got out=%q err=%v", out, err)
	}

	// fix(api)!: y without evidence -> FAIL.
	fixSHA := commitFile(t, repo, "src/api.go", "package src\n", "fix(api)!: y")
	out, err = AuditE2E(t.Context(), repo, nil, true)
	if err == nil || !strings.Contains(out, fixSHA[:12]) || !strings.Contains(out, "fix(api)!: y") {
		t.Fatalf("expected FAIL for fix(api)!: y (%s), got out=%q err=%v", fixSHA[:12], out, err)
	}
}

func TestAuditE2E_SinceBaselineAndNoTagSkip(t *testing.T) {
	// 1. No tag -> stated skip line.
	noTagRepo := initTestGitRepo(t)
	commitFile(t, noTagRepo, "src/a.go", "package src\n", "feat: untagged feature")
	skipOut, skipErr := AuditE2E(t.Context(), noTagRepo, nil, true)
	if skipErr != nil || !strings.HasPrefix(skipOut, "[SKIP]") {
		t.Fatalf("expected [SKIP] when no tag exists, got out=%q err=%v", skipOut, skipErr)
	}

	// 2. Commit before since -> exempt.
	repo := initTestGitRepo(t)
	commitFile(t, repo, "src/init.go", "package src\n", "chore: initial commit")
	runGitCmd(t, repo, "tag", "v0.1.0")
	baselineSHA := commitFile(t, repo, "src/legacy.go", "package src\n", "feat: legacy feature before adoption")
	policy := &config.ReleaseE2EPolicy{Since: baselineSHA}
	out, err := AuditE2E(t.Context(), repo, policy, true)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("expected commit at/before since (%s) to be exempt, got out=%q err=%v", baselineSHA, out, err)
	}
}

func TestJudge_MutantEvidenceAlwaysTrueFailsFirstCase(t *testing.T) {
	repo := initTestGitRepo(t)
	commitFile(t, repo, "src/init.go", "package src\n", "chore: initial commit")
	runGitCmd(t, repo, "tag", "v0.1.0")
	commitFile(t, repo, "src/x.go", "package src\nfunc X() {}\n", "feat: add x")

	prev := evidenceMutantAlwaysTrue
	evidenceMutantAlwaysTrue = true
	defer func() { evidenceMutantAlwaysTrue = prev }()

	_, err := AuditE2E(t.Context(), repo, nil, true)
	if err != nil {
		t.Fatalf("expected mutant (evidence always true) to silently pass and fail the negative check, got err=%v", err)
	}
}

func TestResolveReleaseContext(t *testing.T) {
	if !ResolveReleaseContext(true, nil) {
		t.Fatalf("expected flagRelease=true to enable release context")
	}
	if !ResolveReleaseContext(false, func(k string) string {
		if k == ReleasePREnv {
			return "true"
		}
		return ""
	}) {
		t.Fatalf("expected PRAETOR_RELEASE_PR=true to enable release context")
	}
	if !ResolveReleaseContext(false, func(k string) string {
		if k == GitHubHeadRefEnv {
			return "release-please--branches--main"
		}
		return ""
	}) {
		t.Fatalf("expected GITHUB_HEAD_REF=release-please--... to enable release context")
	}
	if ResolveReleaseContext(false, func(string) string { return "" }) {
		t.Fatalf("expected empty environment and false flag to be non-release context")
	}
}
