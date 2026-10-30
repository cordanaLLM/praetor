// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secretscan_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/secretscan"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets"
	"go.yaml.in/yaml/v3"
)

var testDay = time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)

func writeManifest(t *testing.T, dir string, exceptions []config.Exception) {
	t.Helper()
	manifest := config.Manifest{
		Version:    1,
		Exceptions: exceptions,
	}
	data, err := yaml.Marshal(&manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func defaultOptions(dir string) secretscan.Options {
	return secretscan.Options{
		Dir:     dir,
		Scanner: []string{"gitleaks"},
		Run:     util.RunCommand,
		Now:     func() time.Time { return testDay },
	}
}

func checkPlantedMergeWithoutM(t *testing.T, dir string) {
	t.Helper()
	oldOpts := defaultOptions(dir)
	var oldArgs []string
	oldOpts.Run = func(ctx context.Context, d, name string, args ...string) (string, error) {
		filtered := make([]string, 0, len(args))
		for _, a := range args {
			if strings.HasPrefix(a, "--log-opts=") {
				filtered = append(filtered, "--log-opts=--full-history --all")
			} else {
				filtered = append(filtered, a)
			}
		}
		oldArgs = filtered
		return util.RunCommand(ctx, d, name, filtered...)
	}
	oldReport, err := secretscan.Check(t.Context(), oldOpts)
	if err != nil {
		t.Fatalf("old scan failed: %v", err)
	}
	if oldReport.Failed() || len(oldReport.Blocking) != 0 {
		t.Fatalf("expected old scan without -m to pass, got: %d findings with args %v", len(oldReport.Blocking), oldArgs)
	}
}

func checkPlantedMergeWithM(t *testing.T, dir, mergeHash string) {
	t.Helper()
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !report.Failed() {
		t.Fatal("expected secretscan.Check to fail on merge commit leak")
	}
	if len(report.Blocking) == 0 {
		t.Fatal("expected blocking findings for merge commit leak")
	}
	for _, finding := range report.Blocking {
		if finding.Commit != mergeHash {
			t.Errorf("finding commit = %q, want merge commit %q", finding.Commit, mergeHash)
		}
		if finding.File != "creds.py" {
			t.Errorf("finding file = %q, want creds.py", finding.File)
		}
	}
}

func TestCheck_PlantedMergeOnly_PositiveAndNegative(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, mergeHash := secrets.BuildMergeLeakRepo(t)
	checkPlantedMergeWithoutM(t, dir)
	checkPlantedMergeWithM(t, dir, mergeHash)
}

func checkOrdinaryLeak(t *testing.T, exceptions []config.Exception) *secretscan.Report {
	t.Helper()
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	if exceptions != nil {
		writeManifest(t, dir, exceptions)
	}
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	return report
}

func testExceptionsLifecycle_Unexcused(t *testing.T) {
	report := checkOrdinaryLeak(t, nil)
	if !report.Failed() || len(report.Blocking) != 1 {
		t.Fatalf("expected 1 blocking finding, got %d", len(report.Blocking))
	}
}

func testExceptionsLifecycle_Unexpired(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "creds.py",
			Tool:    "generic-api-key",
			Expires: "2026-11-01",
			Reason:  "temporary test waiver",
		},
	})
	if report.Failed() {
		t.Fatalf("expected pass with unexpired exception, got failed: %s", report.Summary())
	}
	if len(report.Allowed) != 1 || len(report.Blocking) != 0 {
		t.Fatalf("expected 1 allowed and 0 blocking, got allowed=%d, blocking=%d", len(report.Allowed), len(report.Blocking))
	}
}

func testExceptionsLifecycle_Expired(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "creds.py",
			Tool:    "generic-api-key",
			Expires: "2026-10-01",
			Reason:  "expired waiver",
		},
	})
	if !report.Failed() || len(report.Blocking) != 1 {
		t.Fatalf("expected expired exception to refuse finding, got blocking=%d", len(report.Blocking))
	}
}

func testExceptionsLifecycle_Unused(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "other.py",
			Tool:    "generic-api-key",
			Expires: "2026-11-01",
			Reason:  "waiver for other file",
		},
	})
	if !report.Failed() {
		t.Fatal("expected failure on unused exception")
	}
	if len(report.Unused) != 1 {
		t.Fatalf("expected 1 unused exception, got %d", len(report.Unused))
	}
}

func TestCheck_ExceptionsLifecycle_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}

	t.Run("unexcused token is refused", testExceptionsLifecycle_Unexcused)
	t.Run("unexpired exception excuses token", testExceptionsLifecycle_Unexpired)
	t.Run("expired exception excuses nothing", testExceptionsLifecycle_Expired)
	t.Run("unused exception fails", testExceptionsLifecycle_Unused)
}

func TestCheck_CleanRepo_Passes(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)

	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("clean scan error: %v", err)
	}
	if report.Failed() {
		t.Fatalf("clean repo failed: %s", report.Summary())
	}
	want := "zero secret findings in history; no baselined entries (baseline file absent)"
	if report.Summary() != want {
		t.Errorf("summary = %q, want %q", report.Summary(), want)
	}
}

func TestCheck_ErrorHandling(t *testing.T) {
	var nilCtx context.Context
	if _, err := secretscan.Check(nilCtx, secretscan.Options{}); err == nil {
		t.Error("expected error on nil context")
	}
	ctx := t.Context()
	if _, err := secretscan.Check(ctx, secretscan.Options{}); err == nil {
		t.Error("expected error on empty options")
	}
	runnerErr := fmt.Errorf("runner failed")
	mockRunner := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return "stderr output", runnerErr
	}
	_, err := secretscan.Check(ctx, secretscan.Options{
		Dir: t.TempDir(),
		Run: mockRunner,
	})
	if err == nil {
		t.Error("expected error when runner fails")
	}
}

func testForeignRemoteExcluded(t *testing.T, dir string) {
	t.Helper()
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if report.Failed() || len(report.Blocking) != 0 {
		t.Fatalf("expected foreign remote refs to be excluded, got %d blocking findings: %v",
			len(report.Blocking), report.Blocking)
	}
}

func testRefIncluded(t *testing.T, dir, addRef, delRef, leakHash, scopeLabel string) {
	t.Helper()
	secrets.UpdateRef(t, dir, addRef, leakHash)
	secrets.DeleteRef(t, dir, delRef)
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if !report.Failed() || len(report.Blocking) == 0 {
		t.Fatalf("expected blocking finding for secret reachable from %s", scopeLabel)
	}
	if report.Blocking[0].Commit != leakHash {
		t.Errorf("commit = %q, want %q", report.Blocking[0].Commit, leakHash)
	}
}

func TestCheck_RefScoping_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, leakHash := secrets.BuildForeignRemoteLeakRepo(t)

	t.Run("foreign remote refs excluded", func(t *testing.T) {
		testForeignRemoteExcluded(t, dir)
	})
	t.Run("origin remote ref included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/remotes/origin/feature", "refs/remotes/upstream/leak", leakHash, "refs/remotes/origin/feature")
	})
	t.Run("local branch included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/heads/feature", "refs/remotes/origin/feature", leakHash, "local branch")
	})
	t.Run("tag included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/tags/v1.0.0", "refs/heads/feature", leakHash, "tag")
	})
}

func stubReportRunner(reportJSON string, exitErr error) secretscan.Runner {
	return func(_ context.Context, _, _ string, args ...string) (string, error) {
		for i := 0; i < len(args); i++ {
			if args[i] == "--report-path" && i+1 < len(args) {
				if err := os.WriteFile(args[i+1], []byte(reportJSON), 0o600); err != nil {
					return "", err
				}
			}
		}
		return "", exitErr
	}
}

func TestCheck_Baseline_3D(t *testing.T) {
	t.Run("new finding not in baseline fails", testBaselineNewFindingFails)
	t.Run("baselined finding passes and counts in summary", testBaselineMatchedFindingPasses)
	t.Run("stale baseline entry reports stale without failing", testBaselineStaleEntryPasses)
}

func testBaselineNewFindingFails(t *testing.T) {
	dir := t.TempDir()
	base := &baseline.Baseline{
		Version: 1,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-12", FilePath: "creds.py", LineNumber: 1, Message: "generic-api-key", Fingerprint: "synth-fp-old"},
		},
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, secretscan.BaselineFile), base); err != nil {
		t.Fatal(err)
	}
	reportJSON := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":2,"Commit":"aaaa","Fingerprint":"synth-fp-new"}]`
	report, err := secretscan.Check(t.Context(), secretscan.Options{
		Dir: dir,
		Run: stubReportRunner(reportJSON, fmt.Errorf("exit status 1")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Failed() || len(report.Blocking) != 1 || len(report.StaleBaseline) != 1 {
		t.Fatalf("report = %+v; want 1 blocking, 1 stale", report)
	}
}

func testBaselineMatchedFindingPasses(t *testing.T) {
	dir := t.TempDir()
	base := &baseline.Baseline{
		Version: 1,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-12", FilePath: "creds.py", LineNumber: 1, Message: "generic-api-key", Fingerprint: "synth-fp-1"},
		},
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, secretscan.BaselineFile), base); err != nil {
		t.Fatal(err)
	}
	reportJSON := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":1,"Commit":"aaaa","Fingerprint":"synth-fp-1"}]`
	report, err := secretscan.Check(t.Context(), secretscan.Options{
		Dir: dir,
		Run: stubReportRunner(reportJSON, fmt.Errorf("exit status 1")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() || len(report.Baselined) != 1 || len(report.Allowed) != 1 {
		t.Fatalf("report = %+v; want pass with 1 baselined finding", report)
	}
	if got := report.Summary(); got != "zero blocking secret findings (1 baselined)" {
		t.Fatalf("summary = %q, want 'zero blocking secret findings (1 baselined)'", got)
	}
}

func testBaselineStaleEntryPasses(t *testing.T) {
	dir := t.TempDir()
	base := &baseline.Baseline{
		Version: 1,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-12", FilePath: "creds.py", LineNumber: 1, Message: "generic-api-key", Fingerprint: "synth-fp-stale"},
		},
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, secretscan.BaselineFile), base); err != nil {
		t.Fatal(err)
	}
	report, err := secretscan.Check(t.Context(), secretscan.Options{
		Dir: dir,
		Run: stubReportRunner("[]", nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() || len(report.StaleBaseline) != 1 {
		t.Fatalf("report = %+v; want pass with 1 stale baseline entry", report)
	}
	if got := report.Summary(); got != "zero secret findings in history; 1 stale baselined entry(ies)" {
		t.Fatalf("summary = %q, want stale baselined count", got)
	}
}

func TestRecordBaseline_3D(t *testing.T) {
	dir := t.TempDir()
	initial := &baseline.Baseline{
		Version: 1,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-04", FilePath: "main.go", LineNumber: 10, Message: "complexity", Fingerprint: "main.go:HISS-04:fn:f#1"},
		},
	}
	baselinePath := filepath.Join(dir, secretscan.BaselineFile)
	if err := baseline.SaveBaseline(baselinePath, initial); err != nil {
		t.Fatal(err)
	}
	oneFinding := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":4,"Commit":"aaaa","Secret":"SYNTH-DO-NOT-WRITE","Match":"KEY=SYNTH-DO-NOT-WRITE","Fingerprint":"synth-fp-1"}]`
	if _, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: secretscan.Options{Dir: dir, Run: stubReportRunner(oneFinding, fmt.Errorf("exit status 1"))},
	}); err == nil {
		t.Fatal("expected count increase without --reason to be refused")
	}
	res, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: secretscan.Options{Dir: dir, Run: stubReportRunner(oneFinding, fmt.Errorf("exit status 1"))},
		Reason:  "adoption baseline",
	})
	if err != nil {
		t.Fatalf("RecordBaseline with reason failed: %v", err)
	}
	if res.Recorded != 1 || res.Next.Count() != 2 {
		t.Fatalf("recorded = %d, total = %d; want 1 and 2", res.Recorded, res.Next.Count())
	}
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SYNTH-DO-NOT-WRITE") {
		t.Fatalf("secret text leaked into baseline file:\n%s", string(raw))
	}
	twoFindings := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":4,"Commit":"aaaa","Fingerprint":"synth-fp-1"},{"RuleID":"aws-access-token","File":"b.py","StartLine":8,"Commit":"bbbb","Fingerprint":"synth-fp-2"}]`
	if _, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: secretscan.Options{Dir: dir, Run: stubReportRunner(twoFindings, fmt.Errorf("exit status 1"))},
	}); err == nil {
		t.Fatal("expected second record with more findings and no reason to be refused")
	}
}

// Order engine-hiss-unenforced-checks-20261027: secretscan owns HISS-12 baseline entries.
// Recording with zero findings removes stale HISS-12 entries, and recording with findings
// writes the current set only, while preserving entries of other rules (HISS-04).
func TestRecordBaseline_StaleHISS12RemovedAndCurrentWritten(t *testing.T) {
	t.Run("zero findings removes stale", testRecordBaselineZeroFindingsRemovesStale)
	t.Run("findings writes current set only", testRecordBaselineFindingsWritesCurrentOnly)
}

func testRecordBaselineZeroFindingsRemovesStale(t *testing.T) {
	dir := t.TempDir()
	initial := &baseline.Baseline{
		Version:          1,
		Repository:       "acme/widgets",
		TotalInfractions: 2,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-04", FilePath: "main.go", LineNumber: 10, Message: "complexity", Fingerprint: "main.go:HISS-04:fn:f#1"},
			{RuleID: secretscan.BaselineRuleID, FilePath: "old_secret.py", LineNumber: 2, Message: "generic-api-key", Fingerprint: "synth-fp-stale"},
		},
	}
	baselinePath := filepath.Join(dir, secretscan.BaselineFile)
	if err := baseline.SaveBaseline(baselinePath, initial); err != nil {
		t.Fatal(err)
	}

	res, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: secretscan.Options{Dir: dir, Run: stubReportRunner("[]", nil)},
	})
	if err != nil {
		t.Fatalf("RecordBaseline with zero findings failed: %v", err)
	}
	if res.Recorded != 0 || res.Next.Count() != 1 {
		t.Fatalf("expected recorded 0 and total 1 (HISS-04 kept), got recorded %d total %d", res.Recorded, res.Next.Count())
	}
	if len(res.Next.Infractions) != 1 || res.Next.Infractions[0].RuleID != "HISS-04" {
		t.Fatalf("expected only HISS-04 entry, got: %+v", res.Next.Infractions)
	}
}

func testRecordBaselineFindingsWritesCurrentOnly(t *testing.T) {
	dir := t.TempDir()
	initial := &baseline.Baseline{
		Version:          1,
		Repository:       "acme/widgets",
		TotalInfractions: 2,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-04", FilePath: "main.go", LineNumber: 10, Message: "complexity", Fingerprint: "main.go:HISS-04:fn:f#1"},
			{RuleID: secretscan.BaselineRuleID, FilePath: "old_secret.py", LineNumber: 2, Message: "generic-api-key", Fingerprint: "synth-fp-stale"},
		},
	}
	baselinePath := filepath.Join(dir, secretscan.BaselineFile)
	if err := baseline.SaveBaseline(baselinePath, initial); err != nil {
		t.Fatal(err)
	}

	currentFinding := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":4,"Commit":"aaaa","Fingerprint":"synth-fp-current"}]`
	res, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: secretscan.Options{Dir: dir, Run: stubReportRunner(currentFinding, fmt.Errorf("exit status 1"))},
		Reason:  "record current secret finding",
	})
	if err != nil {
		t.Fatalf("RecordBaseline with current finding failed: %v", err)
	}
	if res.Recorded != 1 || res.Next.Count() != 2 {
		t.Fatalf("expected recorded 1 and total 2, got recorded %d total %d", res.Recorded, res.Next.Count())
	}
	for _, inf := range res.Next.Infractions {
		if inf.Fingerprint == "synth-fp-stale" {
			t.Fatalf("stale HISS-12 entry should not be present: %+v", res.Next.Infractions)
		}
	}
}

func TestResolveScanner_PlainBinary_3D(t *testing.T) {
	t.Run("returns lookpath for plain binary", func(t *testing.T) {
		dir := t.TempDir()
		plain := testsupport.BuildFakeGitleaks(t, dir)
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

		got, err := secretscan.ResolveScanner(t.Context())
		if err != nil {
			t.Fatalf("ResolveScanner failed: %v", err)
		}
		if got != plain {
			t.Fatalf("ResolveScanner = %q, want %q", got, plain)
		}
	})
	t.Run("fails when gitleaks missing on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := secretscan.ResolveScanner(t.Context()); err == nil {
			t.Fatal("expected error when gitleaks is not on PATH")
		}
	})
	t.Run("fails on nil context", func(t *testing.T) {
		var nilCtx context.Context
		if _, err := secretscan.ResolveScanner(nilCtx); err == nil {
			t.Fatal("expected error on nil context")
		}
	})
}

func TestResolveScanner_MiseShim_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	normalHome := t.TempDir()
	realDir := t.TempDir()
	realGitleaks := testsupport.BuildFakeGitleaks(t, realDir)
	shimDir := t.TempDir()
	miseBin := testsupport.BuildFakeMise(t, shimDir, normalHome, realGitleaks)
	if err := os.Symlink(miseBin, filepath.Join(shimDir, "gitleaks")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", normalHome)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := secretscan.ResolveScanner(t.Context())
	if err != nil {
		t.Fatalf("ResolveScanner failed: %v", err)
	}
	if got != realGitleaks {
		t.Fatalf("ResolveScanner = %q, want %q", got, realGitleaks)
	}
}

type miseFailureCase struct {
	name       string
	miseSource string
	setup      func(t *testing.T, dir, miseBin string)
	want       string
}

// printSiblingFakeMise makes the fake mise print <its own directory>/other/gitleaks, which setup
// prepares per case; each case names the detail only its own validation branch reports.
const printSiblingFakeMise = "package main\nimport (\"fmt\"; \"os\"; \"path/filepath\")\nfunc main() { fmt.Println(filepath.Join(filepath.Dir(os.Args[0]), \"other\", \"gitleaks\")) }\n"

var miseFailureCases = []miseFailureCase{
	{
		name:       "non-zero exit",
		miseSource: "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Fprintln(os.Stderr, \"failed\"); os.Exit(1) }\n",
		want:       "exit status 1",
	},
	{
		name:       "two lines",
		miseSource: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"/path/to/gitleaks\\nextra line\") }\n",
		want:       "expected exactly one line, got 2 lines",
	},
	{
		name:       "relative path",
		miseSource: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"bin/gitleaks\") }\n",
		want:       "is not absolute",
	},
	{
		name:       "base not gitleaks",
		miseSource: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"/usr/bin/python\") }\n",
		want:       "base name is \"python\"",
	},
	{
		name:       "missing file",
		miseSource: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"/tmp/nonexistent-praetor-test/gitleaks\") }\n",
		want:       "no such file or directory",
	},
	{
		name:       "directory, not a regular file",
		miseSource: printSiblingFakeMise,
		setup: func(t *testing.T, dir, _ string) {
			mkdirAll(t, filepath.Join(dir, "other", "gitleaks"))
		},
		want: "is not a regular file",
	},
	{
		name:       "not executable",
		miseSource: printSiblingFakeMise,
		setup: func(t *testing.T, dir, _ string) {
			mkdirAll(t, filepath.Join(dir, "other"))
			if err := os.WriteFile(filepath.Join(dir, "other", "gitleaks"), []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		want: "is not executable",
	},
	{
		name:       "another mise shim",
		miseSource: printSiblingFakeMise,
		setup: func(t *testing.T, dir, miseBin string) {
			mkdirAll(t, filepath.Join(dir, "other"))
			if err := os.Symlink(miseBin, filepath.Join(dir, "other", "gitleaks")); err != nil {
				t.Fatal(err)
			}
		},
		want: "is also a mise shim",
	},
}

func TestResolveScanner_MiseShim_Failures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	for _, tc := range miseFailureCases {
		t.Run(tc.name, func(t *testing.T) {
			verifyMiseShimFailure(t, tc.miseSource, tc.setup, tc.want)
		})
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
}

func verifyMiseShimFailure(t *testing.T, miseSource string, setup func(t *testing.T, dir, miseBin string), want string) {
	t.Helper()
	dir := t.TempDir()
	miseBin := testsupport.BuildExecutable(t, dir, "mise", miseSource)
	shim := filepath.Join(dir, "gitleaks")
	if err := os.Symlink(miseBin, shim); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(t, dir, miseBin)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := secretscan.ResolveScanner(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "is a mise shim; resolving its executable with \"mise which gitleaks\" failed") || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want the mise shim failure naming %q", err.Error(), want)
	}
}

func TestResolveScanner_PlantedMiseUntrustedProof(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	normalHome := t.TempDir()
	isolatedHome := t.TempDir()
	realDir := t.TempDir()
	realGitleaks := testsupport.BuildFakeGitleaks(t, realDir)
	shimDir := t.TempDir()
	miseBin := testsupport.BuildFakeMise(t, shimDir, normalHome, realGitleaks)
	if err := os.Symlink(miseBin, filepath.Join(shimDir, "gitleaks")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", normalHome)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cleanRepo := secrets.BuildCleanRepo(t)
	isolatedCtx, err := util.WithCommandEnvironment(t.Context(), []string{
		"HOME=" + isolatedHome,
		"PATH=/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Old behaviour (Scanner unset): gitleaks shim fails under isolated HOME
	oldOpts := secretscan.Options{
		Dir: cleanRepo,
		Run: util.RunCommand,
	}
	_, oldErr := secretscan.Check(isolatedCtx, oldOpts)
	if oldErr == nil {
		t.Fatal("expected old behaviour to fail with untrusted config error")
	}
	if !strings.Contains(oldErr.Error(), "are not trusted") {
		t.Fatalf("oldErr = %q, want untrusted error", oldErr.Error())
	}

	// 2. Fixed behaviour: ResolveScanner resolves to real binary bypassing shim
	resolved, err := secretscan.ResolveScanner(isolatedCtx)
	if err != nil {
		t.Fatalf("ResolveScanner failed: %v", err)
	}
	if resolved != realGitleaks {
		t.Fatalf("resolved = %q, want real binary %q", resolved, realGitleaks)
	}
	newOpts := secretscan.Options{
		Dir:     cleanRepo,
		Scanner: []string{resolved},
		Run:     util.RunCommand,
	}
	report, err := secretscan.Check(isolatedCtx, newOpts)
	if err != nil {
		t.Fatalf("Check with resolved scanner failed: %v", err)
	}
	if report.Failed() || len(report.Blocking) != 0 {
		t.Fatalf("expected 0 blocking findings, got: %s", report.Summary())
	}
}

func TestUnexcusedSecretInfractions(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, []config.Exception{
		{
			Rule:        config.ExceptionRuleSecrets,
			Path:        "excused.py",
			Tool:        "generic-api-key",
			Fingerprint: "fp-excused",
			Category:    "A",
			Expires:     "2026-12-31",
			Reason:      "test fixture exception",
		},
	})
	reportJSON := `[
		{"RuleID":"generic-api-key","File":"blocking.py","StartLine":10,"Fingerprint":"fp-blocking"},
		{"RuleID":"generic-api-key","File":"excused.py","StartLine":20,"Fingerprint":"fp-excused"}
	]`
	fakeGitleaks := testsupport.BuildFakeGitleaks(t, dir, reportJSON)
	opts := secretscan.Options{
		Dir:     dir,
		Scanner: []string{fakeGitleaks},
		Run:     util.RunCommand,
		Now:     func() time.Time { return testDay },
	}

	got, err := secretscan.UnexcusedSecretInfractions(t.Context(), opts)
	if err != nil {
		t.Fatalf("UnexcusedSecretInfractions failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 unexcused infraction, got %d: %+v", len(got), got)
	}
	want := baseline.Infraction{
		RuleID:      secretscan.BaselineRuleID,
		FilePath:    "blocking.py",
		LineNumber:  10,
		Message:     "generic-api-key",
		Fingerprint: "fp-blocking",
	}
	if got[0] != want {
		t.Fatalf("got infraction %+v, want %+v", got[0], want)
	}

	recRes, err := secretscan.RecordBaseline(t.Context(), secretscan.RecordOptions{
		Options: opts,
		Reason:  "adoption baseline",
	})
	if err != nil {
		t.Fatalf("RecordBaseline failed: %v", err)
	}
	if recRes.Recorded != len(got) || recRes.Next.Count() != len(got) {
		t.Fatalf("RecordBaseline recorded %d total %d, want %d", recRes.Recorded, recRes.Next.Count(), len(got))
	}
	if len(recRes.Next.Infractions) != 1 || recRes.Next.Infractions[0] != want {
		t.Fatalf("RecordBaseline wrote %+v, want %+v", recRes.Next.Infractions, got)
	}
}

func TestResolveScanner_MiseShim_InferredHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	realHome := t.TempDir()
	isolatedHome := t.TempDir()
	realDir := t.TempDir()
	realGitleaks := testsupport.BuildFakeGitleaks(t, realDir)
	shimDir := filepath.Join(realHome, ".local", "bin")
	mkdirAll(t, shimDir)
	miseDataDir := filepath.Join(realHome, ".local", "share", "mise")
	mkdirAll(t, miseDataDir)

	miseSource := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
)
func main() {
	if len(os.Args) >= 3 && os.Args[1] == "which" && os.Args[2] == "gitleaks" {
		if os.Getenv("HOME") != %q {
			fmt.Fprintln(os.Stderr, "mise ERROR gitleaks is not a mise bin")
			os.Exit(1)
		}
		fmt.Println(%q)
		return
	}
	os.Exit(2)
}
`, realHome, realGitleaks)
	miseBin := testsupport.BuildExecutable(t, shimDir, "mise", miseSource)
	if err := os.Symlink(miseBin, filepath.Join(shimDir, "gitleaks")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", isolatedHome)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := secretscan.ResolveScanner(t.Context())
	if err != nil {
		t.Fatalf("ResolveScanner failed: %v", err)
	}
	if got != realGitleaks {
		t.Fatalf("ResolveScanner = %q, want %q", got, realGitleaks)
	}
}

// TestResolveScanner_MiseShim_AmbientHomeWins: when mise answers under the ambient environment,
// the home inferred from the shim's location is never applied (it is a retry for a moved HOME).
func TestResolveScanner_MiseShim_AmbientHomeWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	shimHome := t.TempDir()
	ambientHome := t.TempDir()
	ambientGitleaks := testsupport.BuildFakeGitleaks(t, t.TempDir())
	inferredGitleaks := testsupport.BuildFakeGitleaks(t, t.TempDir())
	shimDir := filepath.Join(shimHome, ".local", "bin")
	mkdirAll(t, shimDir)
	mkdirAll(t, filepath.Join(shimHome, ".local", "share", "mise"))

	miseSource := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
)
func main() {
	if len(os.Args) >= 3 && os.Args[1] == "which" && os.Args[2] == "gitleaks" {
		if os.Getenv("HOME") == %q {
			fmt.Println(%q)
			return
		}
		fmt.Println(%q)
		return
	}
	os.Exit(2)
}
`, ambientHome, ambientGitleaks, inferredGitleaks)
	miseBin := testsupport.BuildExecutable(t, shimDir, "mise", miseSource)
	if err := os.Symlink(miseBin, filepath.Join(shimDir, "gitleaks")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ambientHome)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := secretscan.ResolveScanner(t.Context())
	if err != nil {
		t.Fatalf("ResolveScanner failed: %v", err)
	}
	if got != ambientGitleaks {
		t.Fatalf("ResolveScanner = %q, want the ambient answer %q (not the inferred-home answer %q)", got, ambientGitleaks, inferredGitleaks)
	}
}
