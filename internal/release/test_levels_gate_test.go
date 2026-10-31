// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/forge"
)

type fakeActionsReader struct {
	byWorkflow map[string]forge.WorkflowRunHistory
	err        error
}

func (f *fakeActionsReader) WorkflowRunHistory(_ context.Context, workflow, _ string) (forge.WorkflowRunHistory, error) {
	if f.err != nil {
		return forge.WorkflowRunHistory{}, f.err
	}
	return f.byWorkflow[workflow], nil
}

func (f *fakeActionsReader) WorkflowPermissions(context.Context) (forge.LiveWorkflowPermissions, error) {
	return forge.LiveWorkflowPermissions{}, f.err
}

func (f *fakeActionsReader) WorkflowRunJobs(context.Context, int64) ([]forge.WorkflowJobRecord, error) {
	return nil, f.err
}

func writeTestRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func setupGoLibraryTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeTestRepoFile(t, repo, "go.mod", "module example.com/lib\n\ngo 1.25.0\n")
	writeTestRepoFile(t, repo, "pkg/client.go", "package lib\n\nfunc Ping() string { return \"pong\" }\n")
	writeTestRepoFile(t, repo, "pkg/client_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestPing(t *testing.T) {}\n")
	writeTestRepoFile(t, repo, ".standards.yaml", "version: 1\nprofiles:\n  - framework\n")
	writeTestRepoFile(t, repo, ".github/workflows/ci.yml",
		"name: CI\non:\n  pull_request:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	return repo
}

// Planted negative + positive (#1733): a go-library temp repository whose resolved profiles
// (framework + library-client) require integration with no matching file is refused in release
// context; the same repository with the test file and a workflow job running it passes.
func TestAuditTestLevels_PlantedGoLibraryIntegrationGapAndPass(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)

	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail {
		t.Fatalf("expected [FAIL] for missing integration test in release context, got %+v, err=%v", rep, err)
	}
	if !slices.Contains(rep.Profiles, "library-client") || !strings.Contains(rep.Detail, "integration") {
		t.Fatalf("expected library-client integration gap in detail, got profiles=%v detail=%q", rep.Profiles, rep.Detail)
	}

	// Non-release context warns without failing.
	warnRep, warnErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: false})
	if warnErr != nil || warnRep.Status != TestLevelStatusWarn {
		t.Fatalf("expected [WARN] outside release context, got %+v, err=%v", warnRep, warnErr)
	}

	// Add the integration test file; the existing ci.yml job runs go test ./..., so it now passes.
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")
	passRep, passErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if passErr != nil || passRep.Status != TestLevelStatusPass {
		t.Fatalf("expected [PASS] once integration test file is present, got %+v, err=%v", passRep, passErr)
	}
}

func TestAuditTestLevels_Negative_JobWithIfFalseIsRefused(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	writeTestRepoFile(t, repo, ".github/workflows/ci.yml",
		"name: CI\non: push\njobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -run TestUnit ./...\n  integration:\n    if: false\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -run TestIntegration ./...\n")
	manifestYAML := `version: 1
profiles:
  - library-client
release:
  test_levels:
    unit:
      paths: ["**/*_test.go"]
      job: "unit"
    integration:
      paths: ["**/*_integration_test.go"]
      job: "integration"
`
	writeTestRepoFile(t, repo, ".standards.yaml", manifestYAML)
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "no runnable workflow job") {
		t.Fatalf("expected job with if: false to be refused, got %+v, err=%v", rep, err)
	}
}

func TestAuditTestLevels_BaselinedGapPassesAndNewGapBesideItFails(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	basePath := filepath.Join(repo, ".standards-baseline.json")
	infraction := baseline.Infraction{
		RuleID:     baseline.ReleaseTestLevelRuleID,
		FilePath:   ".standards.yaml",
		LineNumber: 1,
		Anchor:     "level:library-client:integration",
		Message:    "baselined integration gap",
	}
	infs := []baseline.Infraction{infraction}
	baseline.AssignFingerprints(infs)
	snap := &baseline.Baseline{Version: 1, Repository: "acme/lib", Infractions: infs}
	if err := baseline.SaveBaseline(basePath, snap); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}

	// Only integration is missing, and it is baselined -> passes in release context.
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("expected baselined gap to pass, got %+v, err=%v", rep, err)
	}

	// Remove pkg/client_test.go so unit becomes a new unbaselined gap beside the baselined integration gap -> fails.
	if err := os.Remove(filepath.Join(repo, "pkg", "client_test.go")); err != nil {
		t.Fatalf("remove pkg/client_test.go: %v", err)
	}
	failRep, failErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if failErr == nil || failRep.Status != TestLevelStatusFail || !strings.Contains(failRep.Detail, "unit") {
		t.Fatalf("expected new unbaselined unit gap beside baselined integration gap to fail, got %+v, err=%v", failRep, failErr)
	}
}

func TestAuditTestLevels_Negative_RecordingBaselineAddingAnchorIsRefused(t *testing.T) {
	existing := []baseline.Infraction{{
		RuleID:     baseline.ReleaseTestLevelRuleID,
		FilePath:   ".standards.yaml",
		LineNumber: 1,
		Anchor:     "level:library-client:integration",
		Message:    "existing gap",
	}}
	baseline.AssignFingerprints(existing)
	prev := &baseline.Baseline{Version: 1, Repository: "acme/lib", Infractions: existing, TotalInfractions: 1}

	added := append(slices.Clone(existing), baseline.Infraction{
		RuleID:     baseline.ReleaseTestLevelRuleID,
		FilePath:   ".standards.yaml",
		LineNumber: 1,
		Anchor:     "level:library-client:unit",
		Message:    "new gap",
	})
	baseline.AssignFingerprints(added)

	_, err := baseline.Record(prev, added, baseline.RecordOptions{
		OwnedRules: []string{baseline.ReleaseTestLevelRuleID},
	})
	if !errors.Is(err, baseline.ErrReleaseTestLevelShrinkOnly) {
		t.Fatalf("expected ErrReleaseTestLevelShrinkOnly without AllowIncrease, got %v", err)
	}
	_, err = baseline.Record(prev, added, baseline.RecordOptions{
		AllowIncrease: true,
		Rationale:     "try to grow",
		OwnedRules:    []string{baseline.ReleaseTestLevelRuleID},
	})
	if !errors.Is(err, baseline.ErrReleaseTestLevelShrinkOnly) {
		t.Fatalf("expected ErrReleaseTestLevelShrinkOnly with AllowIncrease, got %v", err)
	}
}

func TestAuditTestLevels_Negative_DeclarationDroppingRequiredLevelFailsValidation(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	// go-library resolves to framework + library-client (which requires unit + integration).
	// Declaring release.test_levels with only unit drops integration and must fail validation.
	writeTestRepoFile(t, repo, ".standards.yaml",
		"version: 1\nprofiles:\n  - framework\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n")
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(err.Error(), "cannot drop required test level \"integration\"") {
		t.Fatalf("expected dropping required level to fail validation, got %+v, err=%v", rep, err)
	}
}

func TestAuditTestLevels_Boundary_OfflineRunHealthAndScheduledDefaultBranchRun(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")

	// Offline / nil ActionsReader reports live run health unmeasured.
	line, rep, err := AuditTestLevelsLine(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err != nil || rep.LiveHealth != LiveRunHealthUnmeasured || !strings.Contains(line, LiveRunHealthUnmeasured) {
		t.Fatalf("expected %q in output when offline, got line=%q rep=%+v err=%v", LiveRunHealthUnmeasured, line, rep, err)
	}

	// Scheduled workflow (no pull_request trigger) is satisfied by latest healthy run on default branch.
	writeTestRepoFile(t, repo, ".github/workflows/ci.yml",
		"name: Nightly\non:\n  schedule:\n    - cron: '0 3 * * *'\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	healthyReader := &fakeActionsReader{
		byWorkflow: map[string]forge.WorkflowRunHistory{
			"ci.yml": {
				Known:     true,
				Total:     1,
				Completed: []forge.WorkflowRunRecord{{Number: 100, Event: "schedule", Conclusion: "success"}},
			},
		},
	}
	liveRep, liveErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{
		FlagRelease: true, Actions: healthyReader, Branch: "main",
	})
	if liveErr != nil || liveRep.Status != TestLevelStatusPass || liveRep.LiveHealth != LiveRunHealthVerified {
		t.Fatalf("expected scheduled workflow with healthy default-branch run to pass, got %+v, err=%v", liveRep, liveErr)
	}
}
