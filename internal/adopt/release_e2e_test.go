// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

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

func TestAdoptionRender_TagWorkflowAndHostedGateEnv(t *testing.T) {
	tagRepo, headSHA := initAdoptReleaseTestRepo(t)
	writeOriginRemote(t, tagRepo, "https://github.com/acme/tag-svc.git")
	mustWrite(t, filepath.Join(tagRepo, ".github", "workflows", "release.yml"),
		"name: Release\non:\n  push:\n    tags:\n      - 'v*'\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo release\n")

	sTag := identitySession(t, tagRepo)
	manifestTag := adoptionManifest(t, sTag)
	if manifestTag.Release == nil || manifestTag.Release.E2E == nil || manifestTag.Release.E2E.Since != headSHA {
		t.Fatalf("expected release.e2e.since=%q for tag workflow repo, got %+v", headSHA, manifestTag.Release)
	}

	rendered := RenderHostedGateReleasePREnv("name: Gate\nconcurrency:\n  group: g\n  cancel-in-progress: true\n")
	if !strings.Contains(rendered, "PRAETOR_RELEASE_PR: ${{ startsWith(github.head_ref, 'release-please--') }}") {
		t.Fatalf("RenderHostedGateReleasePREnv missing PRAETOR_RELEASE_PR env: %s", rendered)
	}
	if len(PriorHostedGateReleasePRDigests()) == 0 {
		t.Fatalf("expected non-empty PriorHostedGateReleasePRDigests ledger")
	}
}
