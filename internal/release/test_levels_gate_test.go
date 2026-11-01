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
	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
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
	_, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	want := fmt.Sprintf("workflow inventory exceeds %d files", maxReleaseWorkflowFiles)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got err=%v, want substring %q", err, want)
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
	rep, err := AuditTestLevels(ctx, repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "integration") {
		t.Fatalf("expected untracked integration test file in Git repo to be refused, got %+v, err=%v", rep, err)
	}

	// Once tracked in the Git index, the integration test satisfies the level.
	if out, gitErr := util.RunGit(ctx, repo, "add", "client_integration_test.go"); gitErr != nil {
		t.Fatalf("git add: %v\n%s", gitErr, out)
	}
	passRep, passErr := AuditTestLevels(ctx, repo, nil, TestLevelsOptions{FlagRelease: true})
	if passErr != nil || passRep.Status != TestLevelStatusPass {
		t.Fatalf("expected tracked integration test file to pass, got %+v, err=%v", passRep, passErr)
	}
}

func TestAuditTestLevels_Negative_FileScanCapOverflowFailsClosed(t *testing.T) {
	origCap := maxRepoScanFiles
	maxRepoScanFiles = 3
	t.Cleanup(func() { maxRepoScanFiles = origCap })

	repo := setupGoLibraryTempRepo(t)
	writeTestRepoFile(t, repo, "z_integration_test.go", "package lib\n")

	// Non-Git walk exceeding maxRepoScanFiles must fail closed with an error instead of truncating.
	if _, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: false}); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected walk scan cap overflow to fail closed, got err=%v", err)
	}

	// Git-backed file list exceeding maxRepoScanFiles must also fail closed.
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
	if _, err := AuditTestLevels(ctx, repo, nil, TestLevelsOptions{FlagRelease: false}); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected git file list cap overflow to fail closed, got err=%v", err)
	}
}

func TestAuditTestLevels_Negative_WorkflowDirReadHonorsContextCancellation(t *testing.T) {
	repo := setupGoLibraryTempRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := loadRepositoryWorkflows(ctx, repo)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on cancelled context, got %v", err)
	}
}

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

	rep, err := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
	if err == nil || rep.Status != TestLevelStatusFail || !strings.Contains(rep.Detail, "integration") {
		t.Fatalf("expected non-test file in integration dir to fail integration level, got rep=%+v, err=%v", rep, err)
	}

	// Adding a recognizable test file inside pkg/integration now satisfies the level.
	writeTestRepoFile(t, repo, "pkg/integration/client_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {}\n")
	passRep, passErr := AuditTestLevels(t.Context(), repo, nil, TestLevelsOptions{FlagRelease: true})
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
		FlagRelease: true,
		Actions:     fakeReader,
		Branch:      "main",
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
				Completed: []forge.WorkflowRunRecord{{Number: 1, Event: "push", Conclusion: "failure"}},
			},
			"z_workflow.yml": {
				Known:     true,
				Total:     1,
				Completed: []forge.WorkflowRunRecord{{Number: 2, Event: "push", Conclusion: "success"}},
			},
		},
	}
	opts := TestLevelsOptions{FlagRelease: true, Actions: fakeReader, Branch: "main"}
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
		FlagRelease: true,
		Actions:     errReader,
		Branch:      "main",
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
