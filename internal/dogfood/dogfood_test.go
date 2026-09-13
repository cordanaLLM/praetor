package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// newHostFixture builds a minimal, self-consistent host repository in a temp directory:
// an AGENTS.md plus the transpiled context targets it must stay in sync with. Tests never
// point at the live checkout, so a HISS infraction elsewhere in the tree cannot fail this
// package, and no test reads the developer's real home directory.
func newHostFixture(t *testing.T, body string) string {
	t.Helper()
	host := t.TempDir()

	agentsPath := filepath.Join(host, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	tr := compiler.NewTranspiler()
	res, err := tr.Compile(agentsPath)
	if err != nil {
		t.Fatalf("compile fixture AGENTS.md: %v", err)
	}
	if err := tr.WriteOutputs(res, host); err != nil {
		t.Fatalf("write fixture context targets: %v", err)
	}
	return host
}

// hermeticOptions returns options that keep a run inside temp directories.
func hermeticOptions(host string) DogfoodOptions {
	return DogfoodOptions{
		HostRepoPath:         host,
		SkipWorkstationAudit: true,
		MaxScanTargets:       5,
	}
}

// newTargetRepo creates a directory that looks like a git repository to the target scan.
func newTargetRepo(t *testing.T, parent, name string) string {
	t.Helper()
	repo := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir target repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module "+name+"\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return repo
}

func TestDogfood_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	host := newHostFixture(t, "# Fixture Harness\n\nRun `praetorctl audit` before concluding a turn.\n")

	reportFile := filepath.Join(tmpDir, "dogfood-report.json")
	opts := hermeticOptions(host)
	opts.ReportPath = reportFile

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("RunDogfood failed: %v", err)
	}

	if !rep.ContextSyncPassed {
		t.Fatalf("expected context sync to pass on the fixture host: %s", rep.ContextSyncError)
	}
	if !rep.SelfAuditPassed {
		t.Fatal("expected self audit to pass on the fixture host")
	}
	if !rep.OverallPassed {
		t.Fatal("expected overall dogfood run to pass")
	}

	data, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("report file not written: %v", err)
	}
	var written DogfoodReport
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("report file is not valid JSON: %v", err)
	}
	if written.HostRepoPath != host || !written.OverallPassed {
		t.Fatalf("report file does not describe the run: %+v", written)
	}
	info, err := os.Stat(reportFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != reportFilePerm {
		t.Fatalf("report mode is %v, want %v", info.Mode().Perm(), reportFilePerm)
	}
}

// TestDogfood_TargetAdoptionSimulated covers the target loop that no test used to reach.
func TestDogfood_TargetAdoptionSimulated(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nTarget adoption fixture.\n")

	targetsDir := t.TempDir()
	newTargetRepo(t, targetsDir, "alpha")
	newTargetRepo(t, targetsDir, "beta")
	if err := os.WriteFile(filepath.Join(targetsDir, "README.md"), []byte("not a repo"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := hermeticOptions(host)
	opts.TargetReposDir = targetsDir

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("RunDogfood failed: %v", err)
	}
	if rep.TargetsEvaluated != 2 || len(rep.TargetResults) != 2 {
		t.Fatalf("expected 2 evaluated targets, got %d: %+v", rep.TargetsEvaluated, rep.TargetResults)
	}
	for _, res := range rep.TargetResults {
		if !res.Passed {
			t.Fatalf("target %s failed: %s", res.RepoName, res.Error)
		}
		if res.Archetype == "" {
			t.Fatalf("target %s has no archetype: %+v", res.RepoName, res)
		}
	}
	for _, name := range []string{".standards.yaml", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(targetsDir, "alpha", name)); !os.IsNotExist(err) {
			t.Fatalf("the default run must simulate, not write %s", name)
		}
	}
}

// TestDogfood_Boundary_NonRepoEntriesDoNotConsumeBudget pins the counting fix: plain files
// in the targets directory must not eat the --max-targets budget.
func TestDogfood_Boundary_NonRepoEntriesDoNotConsumeBudget(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nBudget fixture.\n")

	targetsDir := t.TempDir()
	for _, name := range []string{"a-note.md", "b-note.md", "c-note.md"} {
		if err := os.WriteFile(filepath.Join(targetsDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	newTargetRepo(t, targetsDir, "zzz-repo")

	opts := hermeticOptions(host)
	opts.TargetReposDir = targetsDir
	opts.MaxScanTargets = 1

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("RunDogfood failed: %v", err)
	}
	if rep.TargetsEvaluated != 1 {
		t.Fatalf("expected the single repository to be evaluated, got %d", rep.TargetsEvaluated)
	}
}

// TestDogfood_Negative_MistypedTargetsDir pins that a bad --targets path is an error, not a
// silent success.
func TestDogfood_Negative_MistypedTargetsDir(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nMistyped targets fixture.\n")

	opts := hermeticOptions(host)
	opts.TargetReposDir = filepath.Join(t.TempDir(), "repso")

	rep, err := RunDogfood(ctx, opts)
	if err == nil {
		t.Fatal("a nonexistent targets directory must be reported as an error")
	}
	if rep == nil || rep.OverallPassed {
		t.Fatalf("a failed run must never report OverallPassed: %+v", rep)
	}
}

// TestDogfood_Negative_VerifyOnlyFailsOnDesyncedContext pins the --verify-only contract:
// an out-of-sync host must produce a non-nil error.
func TestDogfood_Negative_VerifyOnlyFailsOnDesyncedContext(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nVerify-only fixture.\n")

	if err := os.WriteFile(filepath.Join(host, "CLAUDE.md"), []byte("# drifted by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := hermeticOptions(host)
	opts.VerifyOnly = true

	rep, err := RunDogfood(ctx, opts)
	if err == nil {
		t.Fatal("verify-only must fail when the context targets are out of sync")
	}
	if !errors.Is(err, ErrGovernanceFailed) {
		t.Fatalf("expected ErrGovernanceFailed, got: %v", err)
	}
	if rep.ContextSyncPassed {
		t.Fatal("the report must record the desynchronised context")
	}
}

func TestDogfood_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunDogfood(ctx, hermeticOptions(t.TempDir()))
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestDogfood_Boundary_EmptyTargets(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nEmpty targets fixture.\n")

	opts := hermeticOptions(host)
	opts.TargetReposDir = t.TempDir()

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("expected nil error on empty targets dir, got: %v", err)
	}
	if rep.TargetsEvaluated != 0 {
		t.Fatalf("expected 0 targets evaluated, got: %d", rep.TargetsEvaluated)
	}
}

// TestDogfood_Negative_UnwritableReportPath covers the report-writing error branch.
func TestDogfood_Negative_UnwritableReportPath(t *testing.T) {
	ctx := context.Background()
	host := newHostFixture(t, "# Fixture Harness\n\nReport fixture.\n")

	opts := hermeticOptions(host)
	opts.ReportPath = filepath.Join(t.TempDir(), "absent-dir", "report.json")

	if _, err := RunDogfood(ctx, opts); err == nil {
		t.Fatal("expected an error when the report path is not writable")
	}
}

func TestDogfood_Remote_ReadinessGrades(t *testing.T) {
	tests := []struct {
		debt      int
		hiss      int
		wantGrade string
	}{
		{debt: 2, hiss: 0, wantGrade: "A"},
		{debt: 15, hiss: 5, wantGrade: "B"},
		{debt: 30, hiss: 25, wantGrade: "C"},
		{debt: 100, hiss: 60, wantGrade: "F"},
	}

	for _, tc := range tests {
		got := calculateReadinessGrade(tc.debt, tc.hiss)
		if got != tc.wantGrade {
			t.Errorf("calculateReadinessGrade(%d, %d) = %s, want %s", tc.debt, tc.hiss, got, tc.wantGrade)
		}
	}
}

// TestDogfood_Remote_RefusesOptionLikeURL pins the argument-injection guard: a URL that
// would be parsed as a git option never reaches git, and the failure names the URL.
func TestDogfood_Remote_RefusesOptionLikeURL(t *testing.T) {
	ctx := context.Background()

	for _, url := range []string{"--upload-pack=touch /tmp/pwned", "-c protocol.ext.allow=always"} {
		res, err := testSingleRemoteAdoption(ctx, url)
		if err != nil {
			t.Fatalf("unexpected fatal error for %q: %v", url, err)
		}
		if res.Passed {
			t.Fatalf("expected refusal for %q", url)
		}
		if !strings.Contains(res.Error, "refusing to clone") {
			t.Fatalf("expected a refusal message for %q, got: %s", url, res.Error)
		}
	}

	// The ext:: transport is refused by git itself (protocol.ext.allow defaults to never);
	// the "--" separator keeps it an operand rather than an option either way.
	res, err := testSingleRemoteAdoption(ctx, "ext::sh -c whoami")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res.Passed || res.Error == "" {
		t.Fatalf("the ext:: transport must not succeed: %+v", res)
	}
}

func TestDogfood_Remote_Negative_EmptyURL(t *testing.T) {
	res, err := testSingleRemoteAdoption(context.Background(), "   ")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res.Passed || res.Error == "" {
		t.Fatalf("an empty URL must fail with a description: %+v", res)
	}
}

// TestDogfood_Remote_Boundary_ClampsAndRecordsSkipped pins the MaxRemoteTargets bound: the
// dropped tail is recorded rather than silently discarded. Every URL is refused before any
// network call, so the test stays hermetic.
func TestDogfood_Remote_Boundary_ClampsAndRecordsSkipped(t *testing.T) {
	ctx := context.Background()
	report := &DogfoodReport{RemoteResults: make([]RemoteAdoptionResult, 0)}

	urls := make([]string, 0, MaxRemoteTargets+3)
	for i := 0; i < MaxRemoteTargets+3; i++ {
		urls = append(urls, "-refused-url")
	}

	if err := testRemoteAdoptions(ctx, urls, report); err != nil {
		t.Fatalf("testRemoteAdoptions failed: %v", err)
	}
	if len(report.RemoteResults) != MaxRemoteTargets {
		t.Fatalf("expected %d results, got %d", MaxRemoteTargets, len(report.RemoteResults))
	}
	if len(report.SkippedRemotes) != 3 {
		t.Fatalf("expected 3 skipped remotes to be recorded, got %d", len(report.SkippedRemotes))
	}
}

func TestDogfood_ComputeOverallPassed(t *testing.T) {
	base := &DogfoodReport{ContextSyncPassed: true, SelfAuditPassed: true}
	if !computeOverallPassed(base) {
		t.Fatal("a clean run must pass")
	}

	withFailedTarget := &DogfoodReport{
		ContextSyncPassed: true,
		SelfAuditPassed:   true,
		TargetResults:     []TargetAdoptionResult{{RepoName: "x", Passed: false}},
	}
	if computeOverallPassed(withFailedTarget) {
		t.Fatal("a failed target adoption must fail the run")
	}

	withFailedRemote := &DogfoodReport{
		ContextSyncPassed: true,
		SelfAuditPassed:   true,
		RemoteResults:     []RemoteAdoptionResult{{RepoURL: "u", Passed: false}},
	}
	if computeOverallPassed(withFailedRemote) {
		t.Fatal("a failed remote benchmark must fail the run")
	}
}

func TestDogfood_Remote_InvalidURL(t *testing.T) {
	ctx := context.Background()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho invalid remote >&2\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	res, err := testSingleRemoteAdoption(ctx, "https://invalid.example.com/nonexistent/repo.git")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure on invalid URL")
	}
	if res.Error == "" {
		t.Fatal("expected error description in result")
	}
}
