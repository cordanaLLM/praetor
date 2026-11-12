// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

type fakeActionsReader struct {
	byWorkflow map[string]forge.WorkflowRunHistory
	jobsByRun  map[int64][]forge.WorkflowJobRecord
	err        error
	jobsErr    error
}

// TestAuditTestLevels_Negative_ReleaseWithoutReader refuses file evidence alone in release mode.
func TestAuditTestLevels_Negative_ReleaseWithoutReader(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "no Actions reader") {
		t.Fatalf("release without live access must refuse every level: report=%+v err=%v", rep, err)
	}
	if len(rep.Findings) != len(rep.Levels) {
		t.Fatalf("got %d findings for %d levels", len(rep.Findings), len(rep.Levels))
	}
}

// WorkflowRunHistory returns stubbed workflow run history for workflow from the fakeActionsReader receiver.
func (f *fakeActionsReader) WorkflowRunHistory(ctx context.Context, workflow, branch string) (forge.WorkflowRunHistory, error) {
	if _, ok := ctx.Deadline(); !ok {
		return forge.WorkflowRunHistory{}, errors.New("history read has no deadline")
	}
	if branch != "main" {
		return forge.WorkflowRunHistory{}, fmt.Errorf("history read branch %q, want main", branch)
	}
	if f.err != nil {
		return forge.WorkflowRunHistory{}, f.err
	}
	return f.byWorkflow[workflow], nil
}

// WorkflowPermissions returns stubbed live workflow permissions or configured errors.
func (f *fakeActionsReader) WorkflowPermissions(context.Context) (forge.LiveWorkflowPermissions, error) {
	return forge.LiveWorkflowPermissions{}, f.err
}

// WorkflowRunJobs returns stubbed workflow job records or configured errors.
func (f *fakeActionsReader) WorkflowRunJobs(ctx context.Context, runID int64) ([]forge.WorkflowJobRecord, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("jobs read has no deadline")
	}
	return f.jobsByRun[runID], errors.Join(f.err, f.jobsErr)
}

// successfulReleaseLevelOptions supplies completed CI and job evidence for the fixture release commit.
func successfulReleaseLevelOptions() TestLevelsOptions {
	const head = "1234567890abcdef1234567890abcdef12345678"
	return TestLevelsOptions{
		FlagRelease: true, Head: head, Branch: "main",
		Actions: &fakeActionsReader{
			byWorkflow: map[string]forge.WorkflowRunHistory{
				"ci.yml": {Known: true, Completed: []forge.WorkflowRunRecord{
					{ID: 42, HeadSHA: head, HeadBranch: "main", Status: "completed", Conclusion: "success"},
				}},
			},
			jobsByRun: map[int64][]forge.WorkflowJobRecord{42: {{Name: "test", Conclusion: "success"}}},
		},
	}
}

// releaseLevelReader extracts the fixture reader while checking the interface assertion.
func releaseLevelReader(t *testing.T, opts TestLevelsOptions) *fakeActionsReader {
	t.Helper()
	reader, ok := opts.Actions.(*fakeActionsReader)
	if !ok {
		t.Fatalf("release fixture has Actions reader %T", opts.Actions)
	}
	return reader
}

// assertReleaseCICase verifies the public audit verdict and each level's release-CI refusal.
func assertReleaseCICase(t *testing.T, opts TestLevelsOptions, want string) {
	t.Helper()
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	rep, err := AuditTestLevels(t.Context(), repo, nil, opts)
	if !strings.Contains(rep.LiveHealth, "release commit CI") {
		t.Fatalf("release path absent from LiveHealth: %q", rep.LiveHealth)
	}
	if want == "" {
		if err != nil || rep.Status != TestLevelStatusPass || len(rep.Findings) != 0 {
			t.Fatalf("successful release CI refused: report=%+v err=%v", rep, err)
		}
		return
	}
	assertReleaseCIRefusal(t, rep, err, want)
}

// assertReleaseCIRefusal verifies that each declared level refuses the named live-evidence gap.
func assertReleaseCIRefusal(t *testing.T, rep TestLevelsReport, err error, want string) {
	t.Helper()
	if err == nil || rep.Status != TestLevelStatusFail || len(rep.Findings) != len(rep.Levels) {
		t.Fatalf("release CI must refuse every level: report=%+v err=%v", rep, err)
	}
	for i := 0; i < len(rep.Findings) && i < config.MaxReleaseTestLevels; i++ {
		if !strings.Contains(rep.Findings[i].Reason, want) || rep.Findings[i].Baselined {
			t.Errorf("finding=%+v; want unbaselined reason containing %q", rep.Findings[i], want)
		}
	}
}

// TestAuditTestLevels_ReleaseCommitRuns rejects missing, unrelated, incomplete, or failed CI runs.
func TestAuditTestLevels_ReleaseCommitRuns(t *testing.T) {
	cases := []struct {
		name   string
		change func(*forge.WorkflowRunHistory)
		want   string
	}{
		{"success", func(*forge.WorkflowRunHistory) {}, ""},
		{"no run", func(h *forge.WorkflowRunHistory) { h.Completed = nil }, "no run of ci.yml for 1234567890ab"},
		{"other commit", func(h *forge.WorkflowRunHistory) { h.Completed[0].HeadSHA = "other" }, "no run of ci.yml for 1234567890ab"},
		{"same prefix", func(h *forge.WorkflowRunHistory) { h.Completed[0].HeadSHA = "1234567890abOTHER" }, "no run of ci.yml for 1234567890ab"},
		{"other branch", func(h *forge.WorkflowRunHistory) { h.Completed[0].HeadBranch = "other" }, "no run of ci.yml for 1234567890ab"},
		{"in progress", func(h *forge.WorkflowRunHistory) { h.Completed[0].Status = "in_progress" }, "no run of ci.yml for 1234567890ab"},
		{"failed run", func(h *forge.WorkflowRunHistory) { h.Completed[0].Conclusion = "failure" }, "run 42 concluded \"failure\""},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			opts := successfulReleaseLevelOptions()
			reader := releaseLevelReader(t, opts)
			history := reader.byWorkflow["ci.yml"]
			cases[i].change(&history)
			reader.byWorkflow["ci.yml"] = history
			assertReleaseCICase(t, opts, cases[i].want)
		})
	}
}

// TestAuditTestLevels_ReleaseCommitJobs requires the matched job, with a successful conclusion.
func TestAuditTestLevels_ReleaseCommitJobs(t *testing.T) {
	cases := []struct {
		name string
		jobs []forge.WorkflowJobRecord
		want string
	}{
		{"missing", nil, "job test missing from run 42"},
		{"unrelated", []forge.WorkflowJobRecord{{Name: "other", Conclusion: "success"}}, "job test missing from run 42"},
		{"failed", []forge.WorkflowJobRecord{{Name: "test", Conclusion: "failure"}}, "job test in run 42 concluded \"failure\""},
		{"skipped", []forge.WorkflowJobRecord{{Name: "test", Conclusion: "skipped"}}, "job test in run 42 concluded \"skipped\""},
		{"unfinished", []forge.WorkflowJobRecord{{Name: "test"}}, "job test in run 42 concluded \"\""},
		{"duplicate failed", []forge.WorkflowJobRecord{{Name: "test", Conclusion: "success"}, {Name: "test", Conclusion: "failure"}}, "job test in run 42 concluded \"failure\""},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			opts := successfulReleaseLevelOptions()
			reader := releaseLevelReader(t, opts)
			reader.jobsByRun[42] = cases[i].jobs
			assertReleaseCICase(t, opts, cases[i].want)
		})
	}
}

// TestAuditTestLevels_ReleaseCommitUnavailable refuses absent inputs and either forge read failure.
func TestAuditTestLevels_ReleaseCommitUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		change func(*TestLevelsOptions, *fakeActionsReader)
		want   string
	}{
		{"no reader", func(o *TestLevelsOptions, _ *fakeActionsReader) { o.Actions = nil }, "no Actions reader"},
		{"no head", func(o *TestLevelsOptions, _ *fakeActionsReader) { o.Head = "" }, "release Head is empty"},
		{"history error", func(_ *TestLevelsOptions, f *fakeActionsReader) { f.err = errors.New("history denied") }, "read release CI runs of ci.yml: history denied"},
		{"jobs error", func(_ *TestLevelsOptions, f *fakeActionsReader) { f.jobsErr = errors.New("jobs denied") }, "read release CI jobs of ci.yml run 42: jobs denied"},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			opts := successfulReleaseLevelOptions()
			cases[i].change(&opts, releaseLevelReader(t, opts))
			assertReleaseCICase(t, opts, cases[i].want)
		})
	}
}

// TestAuditTestLevels_ReleaseMatchedJobName binds an explicit job ID to its API display name.
func TestAuditTestLevels_ReleaseMatchedJobName(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	writeTestRepoFile(t, repo, ".standards.yaml", "version: 1\nprofiles: [library-client]\nrelease:\n  test_levels:\n    unit:\n      paths: ['**/*_test.go']\n      job: unit\n    integration:\n      paths: ['**/*_integration_test.go']\n      job: integration\n")
	writeTestRepoFile(t, repo, ".github/workflows/ci.yml", "on: push\njobs:\n  unit:\n    name: Unit tests\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n  integration:\n    name: Integration tests\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	opts := successfulReleaseLevelOptions()
	reader := releaseLevelReader(t, opts)
	reader.jobsByRun[42] = []forge.WorkflowJobRecord{{Name: "Unit tests", Conclusion: "success"}, {Name: "Integration tests", Conclusion: "success"}}
	rep, err := AuditTestLevels(t.Context(), repo, nil, opts)
	if err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("explicit job ID with display name refused: report=%+v err=%v", rep, err)
	}
	reader.jobsByRun[42] = reader.jobsByRun[42][:1]
	rep, err = AuditTestLevels(t.Context(), repo, nil, opts)
	if err == nil || len(rep.Findings) != 1 || rep.Findings[0].Level != "integration" || !strings.Contains(rep.Detail, "job Integration tests missing") {
		t.Fatalf("another level's successful job must not satisfy integration: report=%+v err=%v", rep, err)
	}
}

// TestAuditTestLevels_ReleaseBaselineCannotWaiveLiveEvidence keeps static anchors from hiding CI refusals.
func TestAuditTestLevels_ReleaseBaselineCannotWaiveLiveEvidence(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	infs := []baseline.Infraction{
		{RuleID: baseline.ReleaseTestLevelRuleID, FilePath: ".standards.yaml", Anchor: "level:library-client:unit"},
		{RuleID: baseline.ReleaseTestLevelRuleID, FilePath: ".standards.yaml", Anchor: "level:library-client:integration"},
	}
	baseline.AssignFingerprints(infs)
	if err := baseline.SaveBaseline(filepath.Join(repo, ".standards-baseline.json"), &baseline.Baseline{Version: 1, Infractions: infs}); err != nil {
		t.Fatalf("save static level anchors: %v", err)
	}
	opts := successfulReleaseLevelOptions()
	reader := releaseLevelReader(t, opts)
	reader.jobsByRun[42] = nil
	rep, err := AuditTestLevels(t.Context(), repo, nil, opts)
	assertReleaseCIRefusal(t, rep, err, "job test missing from run 42")
	opts.Actions = nil
	rep, err = AuditTestLevels(t.Context(), repo, nil, opts)
	assertReleaseCIRefusal(t, rep, err, "no Actions reader")
}

// writeTestRepoFile creates parent directories and writes content to rel under the root test directory.
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

// setupGoLibraryTempRepo initializes a temporary Go library repository with module files, source, tests, and workflows.
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

	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
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
	passRep, passErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if passErr != nil || passRep.Status != TestLevelStatusPass {
		t.Fatalf("expected [PASS] once integration test file is present, got %+v, err=%v", passRep, passErr)
	}
}

// TestAuditTestLevels_Negative_JobWithIfFalseIsRefused proves that workflow jobs guarded by
// if: false are treated as non-runnable and refused by test level audits.
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
	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "no runnable workflow job") {
		t.Fatalf("expected job with if: false to be refused, got %+v, err=%v", rep, err)
	}
}

// TestAuditTestLevels_BaselinedGapPassesAndNewGapBesideItFails proves that baselined test level
// gaps pass audits while new unbaselined gaps fail the release gate.
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
	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("expected baselined gap to pass, got %+v, err=%v", rep, err)
	}

	// Remove pkg/client_test.go so unit becomes a new unbaselined gap beside the baselined integration gap -> fails.
	if err := os.Remove(filepath.Join(repo, "pkg", "client_test.go")); err != nil {
		t.Fatalf("remove pkg/client_test.go: %v", err)
	}
	failRep, failErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if failErr == nil || failRep.Status != TestLevelStatusFail || !strings.Contains(failRep.Detail, "unit") {
		t.Fatalf("expected new unbaselined unit gap beside baselined integration gap to fail, got %+v, err=%v", failRep, failErr)
	}
}

// TestAuditTestLevels_Negative_RecordingBaselineAddingAnchorIsRefused proves that recording
// baselines refuses newly introduced release test level anchors even with allow-increase.
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

// TestAuditTestLevels_Negative_DeclarationDroppingRequiredLevelFailsValidation proves that
// manifest declarations omitting required archetype test levels fail validation.
func TestAuditTestLevels_Negative_DeclarationDroppingRequiredLevelFailsValidation(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	// go-library resolves to framework + library-client (which requires unit + integration).
	// Declaring release.test_levels with only unit drops integration and must fail validation.
	writeTestRepoFile(t, repo, ".standards.yaml",
		"version: 1\nprofiles:\n  - framework\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n")
	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(err.Error(), "cannot drop required test level \"integration\"") {
		t.Fatalf("expected dropping required level to fail validation, got %+v, err=%v", rep, err)
	}
}

// A repository's own .config/archetypes file sets its profiles' required levels: the gate
// honours it, so a declaration that omits a level the shipped catalog requires but the local
// archetype does not passes the drop check (manifest parsing no longer judges it).
func TestAuditTestLevels_Positive_LocalArchetypeLowersRequiredLevels(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	shipped, err := os.ReadFile(filepath.Join("..", "..", ".config", "archetypes", "library-client.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const required = "release_test_levels:\n  - \"unit\"\n  - \"integration\"\n"
	if !strings.Contains(string(shipped), required) {
		t.Fatalf("library-client archetype no longer requires unit and integration in the expected form")
	}
	local := strings.Replace(string(shipped), required, "release_test_levels:\n  - \"unit\"\n", 1)
	writeTestRepoFile(t, repo, ".config/archetypes/library-client.yaml", local)
	writeTestRepoFile(t, repo, ".standards.yaml",
		"version: 1\nprofiles:\n  - library-client\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n")
	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if err != nil && strings.Contains(err.Error(), "cannot drop required test level") {
		t.Fatalf("local archetype ignored: %v (report %+v)", err, rep)
	}
	if !slices.Contains(rep.Levels, "unit") {
		t.Fatalf("levels %v lack unit; the gate did not resolve the local archetype (err %v)", rep.Levels, err)
	}
	if slices.Contains(rep.Levels, "integration") {
		t.Fatalf("levels %v still carry integration from the shipped catalog", rep.Levels)
	}
}

// A workflow directory past the read bound fails the gate instead of judging the first files.
func TestAuditTestLevels_Negative_WorkflowInventoryOverBoundFailsClosed(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	for i := 0; i <= maxReleaseWorkflowFiles; i++ {
		writeTestRepoFile(t, repo, fmt.Sprintf(".github/workflows/w%02d.yml", i),
			"on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	}
	_, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	want := fmt.Sprintf("workflow inventory exceeds %d files", maxReleaseWorkflowFiles)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got err=%v, want substring %q", err, want)
	}
}

// TestAuditTestLevels_Boundary_OfflineRunHealthAndScheduledDefaultBranchRun proves offline
// audits mark run health unmeasured and scheduled workflows are satisfied by default branch runs.
func TestAuditTestLevels_Boundary_OfflineRunHealthAndScheduledDefaultBranchRun(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")

	// Offline / nil ActionsReader reports live run health unmeasured.
	line, rep, err := AuditTestLevelsLine(t.Context(), repo, nil, TestLevelsOptions{})
	if err != nil || !strings.HasPrefix(rep.LiveHealth, LiveRunHealthUnmeasured) || !strings.Contains(line, LiveRunHealthUnmeasured) {
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
		Actions: healthyReader, Branch: "main",
	})
	if liveErr != nil || liveRep.Status != TestLevelStatusPass || !strings.HasPrefix(liveRep.LiveHealth, LiveRunHealthVerified) {
		t.Fatalf("expected scheduled workflow with healthy default-branch run to pass, got %+v, err=%v", liveRep, liveErr)
	}
}

// TestAuditTestLevels_Negative_UntrackedFileInGitRepoDoesNotSatisfyLevel proves that untracked
// files in git repositories do not satisfy release test level path requirements.
func TestAuditTestLevels_Negative_UntrackedFileInGitRepoDoesNotSatisfyLevel(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	ctx := t.Context()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
		{"config", "commit.gpgsign", "false"},
		{"add", "."},
		{"commit", "-q", "-m", "chore: initial tracked files"},
	} {
		if out, err := util.RunGit(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Write an untracked integration test file; it must not satisfy the integration level in a Git repo.
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")
	rep, err := AuditTestLevels(ctx, repo, nil, successfulReleaseLevelOptions())
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "integration") {
		t.Fatalf("expected untracked integration test file in Git repo to be refused, got %+v, err=%v", rep, err)
	}

	// Once tracked in the Git index, the integration test satisfies the level.
	if out, gitErr := util.RunGit(ctx, repo, "add", "client_integration_test.go"); gitErr != nil {
		t.Fatalf("git add: %v\n%s", gitErr, out)
	}
	passRep, passErr := AuditTestLevels(ctx, repo, nil, successfulReleaseLevelOptions())
	if passErr != nil || passRep.Status != TestLevelStatusPass {
		t.Fatalf("expected tracked integration test file to pass, got %+v, err=%v", passRep, passErr)
	}
}

func setMaxRepoScanFilesForTest(t *testing.T, capVal int) {
	t.Helper()
	origCap := maxRepoScanFiles
	maxRepoScanFiles = capVal
	t.Cleanup(func() { maxRepoScanFiles = origCap })
}

func commitAllTestRepoFiles(t *testing.T, repo string) {
	t.Helper()
	runGitCmd(t, repo, "init", "-q")
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "config", "commit.gpgsign", "false")
	runGitCmd(t, repo, "add", ".")
	runGitCmd(t, repo, "commit", "-q", "-m", "chore: initial tracked files")
}

// TestAuditTestLevels_Positive_CandidateCapIgnoresNonCandidates proves that non-candidate files
// exceeding maxRepoScanFiles do not fail the scan and only candidate paths are returned (HISS-15).
func TestAuditTestLevels_Positive_CandidateCapIgnoresNonCandidates(t *testing.T) {
	setMaxRepoScanFilesForTest(t, 3)
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "pkg/helper.go", "package lib\n")
	writeTestRepoFile(t, repo, "docs/guide.md", "# Guide\n")
	writeTestRepoFile(t, repo, "README.md", "# Lib\n")
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")

	_, bindings, err := resolveLevelBindings(t.Context(), repo, nil)
	if err != nil {
		t.Fatalf("resolveLevelBindings: %v", err)
	}
	want := []string{"pkg/client_test.go", "z_integration_test.go"}
	if got, _, _, walkErr := listLevelCandidateFiles(t.Context(), repo, bindings); walkErr != nil || !slices.Equal(got, want) {
		t.Fatalf("walk listLevelCandidateFiles = %v, err=%v; want %v", got, walkErr, want)
	}
	if rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); auditErr != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("walk AuditTestLevels = %+v, err=%v; want PASS", rep, auditErr)
	}

	commitAllTestRepoFiles(t, repo)
	if got, _, _, gitErr := listLevelCandidateFiles(t.Context(), repo, bindings); gitErr != nil || !slices.Equal(got, want) {
		t.Fatalf("git listLevelCandidateFiles = %v, err=%v; want %v", got, gitErr, want)
	}
	if rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); auditErr != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("git AuditTestLevels = %+v, err=%v; want PASS", rep, auditErr)
	}
}

// TestAuditTestLevels_Boundary_CandidatesAtScanCapPass proves that candidate files exactly at
// maxRepoScanFiles pass both filesystem walk and git-backed scans (HISS-15).
func TestAuditTestLevels_Boundary_CandidatesAtScanCapPass(t *testing.T) {
	setMaxRepoScanFilesForTest(t, 3)
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "docs/guide.md", "# Guide\n")
	writeTestRepoFile(t, repo, "pkg/extra_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")

	if rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("boundary walk AuditTestLevels = %+v, err=%v; want PASS", rep, err)
	}
	commitAllTestRepoFiles(t, repo)
	_, bindings, err := resolveLevelBindings(t.Context(), repo, nil)
	if err != nil {
		t.Fatalf("resolveLevelBindings: %v", err)
	}
	if got, _, _, gitErr := listLevelCandidateFiles(t.Context(), repo, bindings); gitErr != nil || len(got) != 3 {
		t.Fatalf("boundary git listLevelCandidateFiles = %v, err=%v; want 3 candidates", got, gitErr)
	}
	if rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); auditErr != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("boundary git AuditTestLevels = %+v, err=%v; want PASS", rep, auditErr)
	}
}

// TestAuditTestLevels_Negative_FileScanCapOverflowFailsClosed proves that candidate file lists
// exceeding maxRepoScanFiles fail closed naming the bound (HISS-15).
func TestAuditTestLevels_Negative_FileScanCapOverflowFailsClosed(t *testing.T) {
	setMaxRepoScanFilesForTest(t, 3)
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "pkg/a_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "pkg/b_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")

	// Non-Git walk exceeding maxRepoScanFiles candidate bound must fail closed naming the bound.
	if _, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: false}); err == nil ||
		!strings.Contains(err.Error(), "scan") || !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "scanned candidate") {
		t.Fatalf("expected walk candidate scan cap overflow naming bound 3, got err=%v", err)
	}

	// Git-backed candidate list exceeding maxRepoScanFiles must also fail closed naming the bound.
	commitAllTestRepoFiles(t, repo)
	if _, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: false}); err == nil ||
		!strings.Contains(err.Error(), "scan") || !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "scanned candidate") {
		t.Fatalf("expected git candidate scan cap overflow naming bound 3, got err=%v", err)
	}
}

// TestAuditTestLevels_Negative_WorkflowDirReadHonorsContextCancellation proves that workflow
// directory loading halts and returns context.Canceled when cancelled.
func TestAuditTestLevels_Negative_WorkflowDirReadHonorsContextCancellation(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := loadRepositoryWorkflows(ctx, repo)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on cancelled context, got %v", err)
	}
}

// TestAuditTestLevels_Negative_SuppliedManifestWithInvalidPolicyFailsValidation proves that
// supplying an invalid manifest to test level auditing fails validation.
func TestAuditTestLevels_Negative_SuppliedManifestWithInvalidPolicyFailsValidation(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	badManifest := &config.Manifest{
		Flavors: []config.FlavorPin{
			{Name: "go-service"},
			{Name: "go-service"},
		},
	}
	rep, err := AuditTestLevels(t.Context(), repo, badManifest, TestLevelsOptions{FlagRelease: false})
	if err == nil || !strings.Contains(err.Error(), "validate supplied manifest") {
		t.Fatalf("expected invalid supplied manifest to fail validation, got rep=%+v, err=%v", rep, err)
	}
}

// Planted negative for Finding 1: directory glob (**/integration/**) alone with no pattern
// must not be satisfied by a tracked non-test file (pkg/integration/README.md).
func TestAuditTestLevels_Negative_DirectoryGlobMatchedNonTestFileAloneDoesNotSatisfyLevel(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	// Write a non-test file in pkg/integration; default integration paths include **/integration/**
	writeTestRepoFile(t, repo, "pkg/integration/README.md", "# Integration docs\n")

	rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "integration") {
		t.Fatalf("expected non-test file in integration dir to fail integration level, got rep=%+v, err=%v", rep, err)
	}

	// Adding a recognizable test file inside pkg/integration now satisfies the level.
	writeTestRepoFile(t, repo, "pkg/integration/client_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")
	passRep, passErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if passErr != nil || passRep.Status != TestLevelStatusPass {
		t.Fatalf("expected recognizable test file in integration dir to pass, got rep=%+v, err=%v", passRep, passErr)
	}
}

// Planted negative for Finding 2: when the gate runs inside the workflow that supplies
// a required level, forge reports workflow health as "self"; this must not be treated as a gap.
func TestAuditTestLevels_Positive_SelfWorkflowRunHealthDoesNotCreateGap(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	t.Setenv("GITHUB_WORKFLOW_REF", "example/lib/.github/workflows/ci.yml@refs/heads/main")
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")

	fakeReader := &fakeActionsReader{
		byWorkflow: map[string]forge.WorkflowRunHistory{
			"ci.yml": {
				Known:     true,
				Total:     1,
				Completed: []forge.WorkflowRunRecord{{Number: 1, Event: "push", Conclusion: "success"}},
			},
		},
	}
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{
		Actions: fakeReader,
		Branch:  "main",
	})
	if err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("expected self-classified workflow run health to not create gap, got rep=%+v, err=%v", rep, err)
	}
}

// Planted negative for Finding 3: parseSingleWorkflowEntry must fail closed with context.Canceled when context is cancelled.
func TestParseSingleWorkflowEntry_Negative_CancelledContextReturnsError(t *testing.T) {
	dir := t.TempDir()
	const workflowContent = "name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	writeTestRepoFile(t, dir, "ci.yml", workflowContent)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("root.Close: %v", closeErr)
		}
	})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected workflow entry")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, parseErr := parseSingleWorkflowEntry(ctx, root, entries[0])
	if !errors.Is(parseErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", parseErr)
	}
}

// Planted negative for Finding 3: parseWorkflowEntries must fail closed with context.Canceled when context is cancelled.
func TestParseWorkflowEntries_Negative_CancelledContextReturnsError(t *testing.T) {
	dir := t.TempDir()
	const workflowContent = "name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	writeTestRepoFile(t, dir, "ci.yml", workflowContent)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("root.Close: %v", closeErr)
		}
	})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected workflow entry")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, parseErr := parseWorkflowEntries(ctx, root, entries)
	if !errors.Is(parseErr, context.Canceled) || !strings.Contains(parseErr.Error(), "scan workflow entries") {
		t.Fatalf("expected scan workflow entries context.Canceled, got %v", parseErr)
	}
}

// createTwoWorkflowRepo sets up a test repository with two workflows created in the requested order.
func createTwoWorkflowRepo(t *testing.T, createFirstA bool) string {
	t.Helper()
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")
	if err := os.Remove(filepath.Join(repo, ".github", "workflows", "ci.yml")); err != nil {
		t.Fatalf("remove default ci.yml: %v", err)
	}
	const wfA = "name: A\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -run TestIntegration ./...\n"
	const wfZ = "name: Z\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -run TestIntegration ./...\n"
	if createFirstA {
		writeTestRepoFile(t, repo, ".github/workflows/a_workflow.yml", wfA)
		writeTestRepoFile(t, repo, ".github/workflows/z_workflow.yml", wfZ)
	} else {
		writeTestRepoFile(t, repo, ".github/workflows/z_workflow.yml", wfZ)
		writeTestRepoFile(t, repo, ".github/workflows/a_workflow.yml", wfA)
	}
	return repo
}

// Planted negative and positive for Finding 2: two workflows running the same level, one healthy
// and one failing, produce the identical deterministic audit result regardless of file creation order.
func TestAuditTestLevels_WorkflowOrderInvariance_CreationOrderDoesNotAffectResult(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")

	fakeReader := &fakeActionsReader{
		byWorkflow: map[string]forge.WorkflowRunHistory{
			"a_workflow.yml": {
				Known:     true,
				Total:     1,
				Completed: []forge.WorkflowRunRecord{{ID: 1, HeadSHA: successfulReleaseLevelOptions().Head, HeadBranch: "main", Status: "completed", Conclusion: "failure"}},
			},
			"z_workflow.yml": {
				Known:     true,
				Total:     1,
				Completed: []forge.WorkflowRunRecord{{Number: 2, Event: "push", Conclusion: "success"}},
			},
		},
	}
	opts := successfulReleaseLevelOptions()
	opts.Actions = fakeReader
	repAZ, errAZ := AuditTestLevels(t.Context(), createTwoWorkflowRepo(t, true), nil, opts)
	repZA, errZA := AuditTestLevels(t.Context(), createTwoWorkflowRepo(t, false), nil, opts)
	if errAZ == nil || errZA == nil {
		t.Fatalf("expected audit error on failing workflow, got errAZ=%v, errZA=%v", errAZ, errZA)
	}
	if repAZ.Status != repZA.Status {
		t.Fatalf("status mismatch across creation orders: AZ=%s, ZA=%s", repAZ.Status, repZA.Status)
	}
	if repAZ.Detail != repZA.Detail {
		t.Fatalf("detail mismatch across creation orders: AZ=%q, ZA=%q", repAZ.Detail, repZA.Detail)
	}
	if repAZ.Status != TestLevelStatusFail || !strings.Contains(repAZ.Detail, "a_workflow.yml") {
		t.Fatalf("expected deterministic failure on a_workflow.yml, got status=%s detail=%q", repAZ.Status, repAZ.Detail)
	}
}

// Planted negative for order engine-test-levels-gate-20261027 item 2: an Actions reader that
// errors must yield a LiveHealth status that explicitly names the underlying error.
func TestAuditTestLevels_Negative_ActionsReaderErrorNamesReason(t *testing.T) {
	t.Setenv("GITHUB_REF", "")
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("PRAETOR_RELEASE_PR", "")
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "client_integration_test.go", "package lib\n")
	writeTestRepoFile(t, repo, ".github/workflows/ci.yml",
		"name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")

	queryErr := errors.New("actions API 503 Service Unavailable")
	errReader := &fakeActionsReader{err: queryErr}

	line, rep, err := AuditTestLevelsLine(t.Context(), repo, nil, TestLevelsOptions{
		Actions: errReader,
		Branch:  "main",
	})
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	wantPrefix := LiveRunHealthUnmeasured + ": "
	if !strings.HasPrefix(rep.LiveHealth, wantPrefix) {
		t.Fatalf("expected LiveHealth to start with %q, got %q", wantPrefix, rep.LiveHealth)
	}
	if !strings.Contains(rep.LiveHealth, queryErr.Error()) {
		t.Fatalf("expected LiveHealth %q to contain error %q", rep.LiveHealth, queryErr.Error())
	}
	if !strings.Contains(line, queryErr.Error()) {
		t.Fatalf("expected formatted line %q to contain error %q", line, queryErr.Error())
	}
}

// setMaxRepoListingBufferBytesForTest lowers the listing buffer bound for one test and restores it afterwards.
func setMaxRepoListingBufferBytesForTest(t *testing.T, limit int) {
	t.Helper()
	orig := maxRepoListingBufferBytes
	maxRepoListingBufferBytes = limit
	t.Cleanup(func() { maxRepoListingBufferBytes = orig })
}

// setMaxRepoListingEntriesForTest lowers the listing entry bound for one test and restores it afterwards.
func setMaxRepoListingEntriesForTest(t *testing.T, limit int) {
	t.Helper()
	orig := maxRepoListingEntries
	maxRepoListingEntries = limit
	t.Cleanup(func() { maxRepoListingEntries = orig })
}

// writeNonCandidateTrackedDocs tracks count documentation files no test level matches, to grow the listing without candidates.
func writeNonCandidateTrackedDocs(t *testing.T, repo string, count int) {
	t.Helper()
	for i := 0; i < count && i < 256; i++ {
		rel := fmt.Sprintf("docs/guide_section_%02d.txt", i)
		writeTestRepoFile(t, repo, rel, "non-candidate documentation\n")
	}
}

// TestAuditTestLevels_LargeListingPassesCandidateCap proves a tracked listing larger than the
// in-memory listing buffer bound still passes when few candidates match, while candidates past
// maxRepoScanFiles fail with the cap error (HISS-15).
func TestAuditTestLevels_LargeListingPassesCandidateCap(t *testing.T) {
	setMaxRepoListingBufferBytesForTest(t, 256)
	setMaxRepoScanFilesForTest(t, 3)
	repo := setupGoLibraryTempRepo(t)
	writeNonCandidateTrackedDocs(t, repo, 30)
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")
	commitAllTestRepoFiles(t, repo)

	_, bindings, err := resolveLevelBindings(t.Context(), repo, nil)
	if err != nil {
		t.Fatalf("resolveLevelBindings: %v", err)
	}
	want := []string{"pkg/client_test.go", "z_integration_test.go"}
	if got, _, _, listErr := listLevelCandidateFiles(t.Context(), repo, bindings); listErr != nil || !slices.Equal(got, want) {
		t.Fatalf("listLevelCandidateFiles = %v, err=%v; want %v", got, listErr, want)
	}
	if rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); auditErr != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("AuditTestLevels = %+v, err=%v; want PASS", rep, auditErr)
	}

	writeTestRepoFile(t, repo, "pkg/extra_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "pkg/fourth_test.go", "package lib\n")
	runGitCmd(t, repo, "add", ".")
	runGitCmd(t, repo, "commit", "-q", "-m", "chore: exceed candidate cap")
	if _, capErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: false}); capErr == nil ||
		!strings.Contains(capErr.Error(), "3") || !strings.Contains(capErr.Error(), "scanned candidate") {
		t.Fatalf("expected candidate cap error naming 3, got %v", capErr)
	}
}

// TestAuditTestLevels_StreamListingBound_3D verifies that a listing just under maxRepoListingEntries
// passes while reaching maxRepoListingEntries fails naming the bound (HISS-15).
func TestAuditTestLevels_StreamListingBound_3D(t *testing.T) {
	setMaxRepoListingEntriesForTest(t, 7)
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")
	commitAllTestRepoFiles(t, repo)

	// setupGoLibraryTempRepo writes 5 files + z_integration_test.go = 6 tracked entries (just under 7).
	if rep, err := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); err != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("expected 6 tracked entries (< 7) to pass, got rep=%+v err=%v", rep, err)
	}

	// Adding 1 file reaches 7 tracked entries -> fails naming maxRepoListingEntries and 7.
	writeTestRepoFile(t, repo, "docs/seventh.txt", "seven\n")
	runGitCmd(t, repo, "add", ".")
	runGitCmd(t, repo, "commit", "-q", "-m", "chore: reach stream bound")
	_, boundErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if boundErr == nil || !strings.Contains(boundErr.Error(), "maxRepoListingEntries") || !strings.Contains(boundErr.Error(), "7") {
		t.Fatalf("expected stream bound error naming maxRepoListingEntries and 7, got %v", boundErr)
	}
}

// TestAuditTestLevels_QuotedAndSpacePrefixedTestFiles proves git ls-files -z preserves non-ASCII
// names git would quote and leading-space paths without trimming (HISS-15).
func TestAuditTestLevels_QuotedAndSpacePrefixedTestFiles(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	if err := os.Remove(filepath.Join(repo, "pkg", "client_test.go")); err != nil {
		t.Fatalf("remove default unit test: %v", err)
	}
	writeTestRepoFile(t, repo, "测试_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestUnicode(t *testing.T) {}\n")
	writeTestRepoFile(t, repo, " leading_integration_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestLeadingSpace(t *testing.T) {}\n")
	commitAllTestRepoFiles(t, repo)

	_, bindings, err := resolveLevelBindings(t.Context(), repo, nil)
	if err != nil {
		t.Fatalf("resolveLevelBindings: %v", err)
	}
	want := []string{" leading_integration_test.go", "测试_test.go"}
	got, _, _, listErr := listLevelCandidateFiles(t.Context(), repo, bindings)
	if listErr != nil || !slices.Equal(got, want) {
		t.Fatalf("listLevelCandidateFiles = %q, err=%v; want %q", got, listErr, want)
	}
	if rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions()); auditErr != nil || rep.Status != TestLevelStatusPass {
		t.Fatalf("expected quoted and space-prefixed test files to satisfy levels, got rep=%+v err=%v", rep, auditErr)
	}
}

// TestAuditTestLevels_CancelledContextStopsWalkStreamAndRustCheck verifies that cancelling ctx
// stops the filesystem walk, the git stream, and directory-glob Rust checks with context.Canceled
// before reading further files (HISS-15).
func TestAuditTestLevels_CancelledContextStopsWalkStreamAndRustCheck(t *testing.T) {
	t.Run("stops non-git candidate walk before reading further", func(t *testing.T) {
		assertReleaseWalkStopsOnCancel(t)
	})
	t.Run("stops git listing stream before reading further", func(t *testing.T) {
		assertReleaseStreamStopsOnCancel(t)
	})
	t.Run("stops directory-glob Rust check with context error", func(t *testing.T) {
		assertReleaseRustCheckStopsOnCancel(t)
	})
}

// assertReleaseWalkStopsOnCancel checks that the non-git candidate walk stops with the context error.
func assertReleaseWalkStopsOnCancel(t *testing.T) {
	t.Helper()
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "pkg/a_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "pkg/b_test.go", "package lib\n")
	ctx, cancel := context.WithCancel(t.Context())
	seen := 0
	_, err := listRepositoryFiles(ctx, repo, func(string) (bool, error) {
		seen++
		cancel()
		return true, nil
	})
	if !errors.Is(err, context.Canceled) || seen != 1 {
		t.Fatalf("walk err=%v seen=%d; want context.Canceled after 1 file", err, seen)
	}
}

// assertReleaseStreamStopsOnCancel checks that the git listing stream stops before reading further paths once the context is cancelled.
func assertReleaseStreamStopsOnCancel(t *testing.T) {
	t.Helper()
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "pkg/a_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "pkg/b_test.go", "package lib\n")
	commitAllTestRepoFiles(t, repo)
	ctx, cancel := context.WithCancel(t.Context())
	seen := 0
	_, err := listRepositoryFiles(ctx, repo, func(string) (bool, error) {
		seen++
		cancel()
		return true, nil
	})
	if !errors.Is(err, context.Canceled) || seen != 1 {
		t.Fatalf("stream err=%v seen=%d; want context.Canceled after 1 file", err, seen)
	}
}

// assertReleaseRustCheckStopsOnCancel checks that the Rust test-file check honours a cancelled context.
func assertReleaseRustCheckStopsOnCancel(t *testing.T) {
	t.Helper()
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "pkg/integration/a.rs", "#[cfg(test)]\nmod tests {}\n")
	writeTestRepoFile(t, repo, "pkg/integration/b.rs", "#[cfg(test)]\nmod tests {}\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	spec := config.ReleaseTestLevelSpec{Paths: []string{"**/integration/**"}}
	matched, err := matchFilesForLevel(ctx, repo, []string{"pkg/integration/a.rs", "pkg/integration/b.rs"}, spec)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("expected context.Canceled and 0 matched files from Rust check, got matched=%v err=%v", matched, err)
	}
}

// TestListLevelCandidateFiles_RefusedRustSourceIsSkippedWithReason plants a tracked symlink leaf under a
// test directory: the snapshot reader refuses it, so it is not a candidate, it is reported as skipped with
// its path and reason, the audit detail names it without failing, and a cancelled context still returns
// the context error (HISS-15).
func TestListLevelCandidateFiles_RefusedRustSourceIsSkippedWithReason(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")
	writeTestRepoFile(t, repo, "pkg/test/real.rs", "#[cfg(test)]\nmod tests {}\n")
	if err := os.Symlink("real.rs", filepath.Join(repo, "pkg", "test", "link.rs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	commitAllTestRepoFiles(t, repo)
	_, bindings, err := resolveLevelBindings(t.Context(), repo, nil)
	if err != nil {
		t.Fatalf("resolveLevelBindings: %v", err)
	}
	assertRefusedRustSkipped(t, repo, bindings)
	rep, auditErr := AuditTestLevels(t.Context(), repo, nil, successfulReleaseLevelOptions())
	if auditErr != nil || rep.SkippedTotal != 1 || !strings.Contains(rep.Detail, "pkg/test/link.rs") || !strings.Contains(rep.Detail, "skipped 1 unreadable Rust source") {
		t.Fatalf("AuditTestLevels = %+v, err=%v; want no failure and the skip named in the detail", rep, auditErr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, ctxErr := listLevelCandidateFiles(ctx, repo, bindings); !errors.Is(ctxErr, context.Canceled) {
		t.Fatalf("cancelled listLevelCandidateFiles err = %v; want context.Canceled", ctxErr)
	}
}

// assertRefusedRustSkipped checks that the candidate listing keeps the readable Rust test file and reports the refused symlink once with its reason.
func assertRefusedRustSkipped(t *testing.T, repo string, bindings []resolvedLevelBinding) {
	t.Helper()
	files, skipped, total, err := listLevelCandidateFiles(t.Context(), repo, bindings)
	if err != nil {
		t.Fatalf("listLevelCandidateFiles: %v", err)
	}
	if !slices.Contains(files, "pkg/test/real.rs") || slices.Contains(files, "pkg/test/link.rs") {
		t.Fatalf("candidates = %v; want real.rs kept and the refused link.rs dropped", files)
	}
	if total != 1 || len(skipped) != 1 || skipped[0].Path != "pkg/test/link.rs" || !strings.Contains(skipped[0].Reason, "refused") {
		t.Fatalf("skipped = %+v (total %d); want link.rs once with its refusal reason", skipped, total)
	}
}

// levelsByCount returns n distinct synthetic test level names.
func levelsByCount(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s%02d", prefix, i))
	}
	return out
}

// TestMergeProfileAndDeclaredLevels_CombinedLimit proves the combined binding limit is enforced once at
// resolution: 33 bindings from profile defaults plus declared extras fail naming the limit and the count,
// exactly 32 pass, and matching never fails per file on the binding count (HISS-15).
func TestMergeProfileAndDeclaredLevels_CombinedLimit(t *testing.T) {
	manifestWith := func(declared int) *config.Manifest {
		levels := map[string]config.ReleaseTestLevelSpec{}
		for _, lvl := range levelsByCount("declared", declared) {
			levels[lvl] = config.ReleaseTestLevelSpec{Paths: []string{"**/*_test.go"}}
		}
		return &config.Manifest{Release: &config.ReleasePolicy{TestLevels: levels}}
	}
	profiles := []string{"p"}
	t.Run("negative 33 combined bindings fail naming limit and count", func(t *testing.T) {
		_, err := mergeProfileAndDeclaredLevels(profiles, map[string][]string{"p": levelsByCount("lvl", 20)}, manifestWith(13))
		if err == nil || !strings.Contains(err.Error(), "33") || !strings.Contains(err.Error(), "limit is 32") {
			t.Fatalf("err = %v; want a failure naming 33 bindings and the limit 32", err)
		}
	})
	t.Run("boundary 32 combined bindings pass", func(t *testing.T) {
		got, err := mergeProfileAndDeclaredLevels(profiles, map[string][]string{"p": levelsByCount("lvl", 20)}, manifestWith(12))
		if err != nil || len(got) != config.MaxReleaseTestLevels {
			t.Fatalf("got %d bindings, err=%v; want %d and no error", len(got), err, config.MaxReleaseTestLevels)
		}
	})
	t.Run("positive matching never fails on the binding count", func(t *testing.T) {
		bindings := make([]resolvedLevelBinding, 0, config.MaxReleaseTestLevels+1)
		for _, lvl := range levelsByCount("lvl", config.MaxReleaseTestLevels+1) {
			bindings = append(bindings, buildLevelBinding("p", lvl, nil))
		}
		if ok, err := fileMatchesAnyBinding(t.Context(), t.TempDir(), "pkg/a_test.go", bindings); err != nil || ok {
			t.Fatalf("fileMatchesAnyBinding over %d bindings = (%v, %v); want no error", len(bindings), ok, err)
		}
	})
}
