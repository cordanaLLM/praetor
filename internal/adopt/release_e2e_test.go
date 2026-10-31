// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/util"
)

func initAdoptReleaseTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGitInAdoptReleaseTest(t, dir, "init")
	runGitInAdoptReleaseTest(t, dir, "config", "user.name", "Test User")
	runGitInAdoptReleaseTest(t, dir, "config", "user.email", "test@example.com")
	runGitInAdoptReleaseTest(t, dir, "config", "commit.gpgsign", "false")
	mustWrite(t, filepath.Join(dir, "README.md"), "# Test\n")
	runGitInAdoptReleaseTest(t, dir, "add", "README.md")
	runGitInAdoptReleaseTest(t, dir, "commit", "-m", "chore: initial commit")
	head := runGitInAdoptReleaseTest(t, dir, "rev-parse", "HEAD")
	return dir, head
}

func runGitInAdoptReleaseTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := util.RunGit(t.Context(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func TestAdoptionRender_ReleaseE2ESection(t *testing.T) {
	// 1. Repository with release-please config gets release.e2e section with since = HEAD.
	withReleaseRepo, headSHA := initAdoptReleaseTestRepo(t)
	writeOriginRemote(t, withReleaseRepo, "https://github.com/acme/release-svc.git")
	mustWrite(t, filepath.Join(withReleaseRepo, "release-please-config.json"), `{"packages":{".":{}}}`)

	sWith := identitySession(t, withReleaseRepo)
	manifestWith := adoptionManifest(t, sWith)
	if manifestWith.Release == nil || manifestWith.Release.E2E == nil {
		t.Fatalf("expected release.e2e section for repository with release-please-config.json, got %+v", manifestWith.Release)
	}
	if manifestWith.Release.E2E.Since != headSHA {
		t.Fatalf("release.e2e.since = %q, want HEAD %q", manifestWith.Release.E2E.Since, headSHA)
	}

	// 2. Repository without release workflow gets none.
	noReleaseRepo, _ := initAdoptReleaseTestRepo(t)
	writeOriginRemote(t, noReleaseRepo, "https://github.com/acme/plain-svc.git")

	sWithout := identitySession(t, noReleaseRepo)
	manifestWithout := adoptionManifest(t, sWithout)
	if manifestWithout.Release != nil {
		t.Fatalf("expected nil release section for repository without release workflow, got %+v", manifestWithout.Release)
	}
}

func TestAdoptionRender_TagWorkflowAndResolveGitCommitError(t *testing.T) {
	tagRepo, headSHA := initAdoptReleaseTestRepo(t)
	writeOriginRemote(t, tagRepo, "https://github.com/acme/tag-svc.git")
	mustWrite(t, filepath.Join(tagRepo, ".github", "workflows", "release.yml"),
		"name: Release\non:\n  push:\n    tags:\n      - 'v*'\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo release\n")

	sTag := identitySession(t, tagRepo)
	manifestTag := adoptionManifest(t, sTag)
	if manifestTag.Release == nil || manifestTag.Release.E2E == nil || manifestTag.Release.E2E.Since != headSHA {
		t.Fatalf("expected release.e2e.since=%q for tag workflow repo, got %+v", headSHA, manifestTag.Release)
	}

	// ResolveGitCommit failure -> write no release.e2e section and record report warning line naming error.
	brokenRepo := t.TempDir()
	mustWrite(t, filepath.Join(brokenRepo, "release-please-config.json"), `{"packages":{".":{}}}`)
	sBroken := &adoptSession{
		repoPath: brokenRepo,
		report:   &AdoptReport{},
	}
	if got := sBroken.releasePolicy(t.Context()); got != nil {
		t.Fatalf("expected nil releasePolicy when ResolveGitCommit fails, got %+v", got)
	}
	if len(sBroken.report.Warnings) != 1 || !strings.Contains(sBroken.report.Warnings[0], "no release.e2e section recorded:") ||
		!strings.Contains(sBroken.report.Warnings[0], "resolve commit HEAD") {
		t.Fatalf("expected warning naming ResolveGitCommit error, got %+v", sBroken.report.Warnings)
	}
}

func TestAdoptReconcileReleaseTestLevelsBaseline_RecordsInitialGaps(t *testing.T) {
	repo, headSHA := initAdoptReleaseTestRepo(t)
	writeOriginRemote(t, repo, "https://github.com/acme/release-lib.git")
	mustWrite(t, filepath.Join(repo, "release-please-config.json"), `{"packages":{".":{}}}`)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module github.com/acme/release-lib\n\ngo 1.25.0\n")
	mustWrite(t, filepath.Join(repo, "pkg", "lib.go"), "package lib\n")
	mustWrite(t, filepath.Join(repo, "pkg", "lib_test.go"), "package lib\n\nimport \"testing\"\n\nfunc TestLib(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(repo, ".github", "workflows", "ci.yml"),
		"name: CI\non: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	runGitInAdoptReleaseTest(t, repo, "add", ".")
	// writeOriginRemote rewrote .git/config, so the identity set by initAdoptReleaseTestRepo is gone;
	// pass it on the command so the commit never depends on the host's git config.
	runGitInAdoptReleaseTest(t, repo, "-c", "user.name=Test User", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "commit", "-m", "feat: add go lib")

	s := identitySession(t, repo)
	s.arch = "framework"
	s.opts.RecordBaseline = true
	s.report.DebtBreakdown = map[string]int{}
	basePath := filepath.Join(repo, baselineFile)
	initial := &baseline.Baseline{Version: 1, Repository: "acme/release-lib", CommitSHA: headSHA, Infractions: []baseline.Infraction{}}
	if err := baseline.SaveBaseline(basePath, initial); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	if err := s.reconcileReleaseTestLevelsBaseline(t.Context(), basePath, initial); err != nil {
		t.Fatalf("reconcileReleaseTestLevelsBaseline: %v", err)
	}
	recorded, err := baseline.LoadBaseline(basePath)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if len(recorded.Infractions) != 1 || recorded.Infractions[0].RuleID != baseline.ReleaseTestLevelRuleID ||
		recorded.Infractions[0].Anchor != "level:library-client:integration" {
		t.Fatalf("expected 1 RELEASE-TEST-LEVEL integration baseline entry, got %+v", recorded.Infractions)
	}
}
