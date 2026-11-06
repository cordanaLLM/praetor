// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

type fakeJobTimeoutReader struct {
	histories   map[string]WorkflowRunHistory
	historyErrs map[string]error
	jobs        map[int64][]WorkflowJobRecord
	jobsErrs    map[int64]error
	jobsCalls   []int64
}

func (f *fakeJobTimeoutReader) WorkflowPermissions(context.Context) (LiveWorkflowPermissions, error) {
	return LiveWorkflowPermissions{}, ErrNotImplemented
}

func (f *fakeJobTimeoutReader) WorkflowRunHistory(_ context.Context, workflow, _ string) (WorkflowRunHistory, error) {
	if f.historyErrs != nil {
		if err := f.historyErrs[workflow]; err != nil {
			return WorkflowRunHistory{}, err
		}
	}
	return f.histories[workflow], nil
}

func (f *fakeJobTimeoutReader) WorkflowRunJobs(_ context.Context, runID int64) ([]WorkflowJobRecord, error) {
	f.jobsCalls = append(f.jobsCalls, runID)
	if f.jobsErrs != nil {
		if err := f.jobsErrs[runID]; err != nil {
			return nil, err
		}
	}
	return f.jobs[runID], nil
}

func writeWorkflow(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jobDurationRecord(name string, durationMinutes float64) WorkflowJobRecord {
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	dur := time.Duration(durationMinutes * float64(time.Minute))
	return WorkflowJobRecord{
		Name:        name,
		StartedAt:   start.Format(time.RFC3339),
		CompletedAt: start.Add(dur).Format(time.RFC3339),
		Conclusion:  "success",
	}
}

// A job without timeout-minutes warns; a reusable-call job does not; an expression value is named not judged.
func TestJobTimeout_Finite_MissingReusableExpression(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n  missing:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 1\n" +
		"  reuse:\n    uses: acme/repo/.github/workflows/reusable.yml@v1\n" +
		"  expr:\n    timeout-minutes: ${{ inputs.timeout }}\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 2\n"
	writeWorkflow(t, root, "ci.yml", content)
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[WARN] job-timeout: .github/workflows/ci.yml:3 job missing: declares no timeout-minutes") {
		t.Errorf("missing timeout-minutes must warn; output:\n%s", out)
	}
	if strings.Contains(out, "job reuse") {
		t.Errorf("reusable job must be exempt; output:\n%s", out)
	}
	if !strings.Contains(out, "[INFO] job-timeout: .github/workflows/ci.yml:10 job expr: timeout-minutes is an expression (${{ inputs.timeout }}), not judged") {
		t.Errorf("expression value must be named not judged; output:\n%s", out)
	}
}

// Static path: 330 warns, 120 passes (boundary), 121 warns.
func TestJobTimeout_Static_CeilingAndBoundaries(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n" +
		"  high:\n    timeout-minutes: 330\n    steps:\n      - run: echo high\n" +
		"  boundary:\n    timeout-minutes: 120\n    steps:\n      - run: echo 120\n" +
		"  above:\n    timeout-minutes: 121\n    steps:\n      - run: echo 121\n"
	writeWorkflow(t, root, "static.yml", content)
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "job high: timeout-minutes 330 is above 120m bound") {
		t.Errorf("330 minutes must warn; output:\n%s", out)
	}
	if strings.Contains(out, "job boundary") {
		t.Errorf("120 minutes boundary must pass; output:\n%s", out)
	}
	if !strings.Contains(out, "job above: timeout-minutes 121 is above 120m bound") {
		t.Errorf("121 minutes must warn; output:\n%s", out)
	}
}

// Measured (fake reader): S = 44.8 with a declared 45 warns as cancel risk (45 < 56);
// S = 10 with 300 warns as runaway (300 > 40); S = 10 with 40 passes (boundary);
// S = 5 with 30 passes (the 30-minute floor of the ceiling).
func TestJobTimeout_Measured_CancelRiskAndRunaway(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n" +
		"  gate:\n    name: Standards\n    timeout-minutes: 45\n    steps:\n      - run: echo gate\n" +
		"  runaway:\n    name: Runaway\n    timeout-minutes: 300\n    steps:\n      - run: echo runaway\n" +
		"  boundary:\n    name: Boundary\n    timeout-minutes: 40\n    steps:\n      - run: echo boundary\n" +
		"  floor:\n    name: Floor\n    timeout-minutes: 30\n    steps:\n      - run: echo floor\n"
	writeWorkflow(t, root, "ci.yml", content)
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"ci.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 1, Conclusion: "success"}}},
		},
		jobs: map[int64][]WorkflowJobRecord{
			1: {
				jobDurationRecord("Standards", 44.8),
				jobDurationRecord("Runaway", 10.0),
				jobDurationRecord("Boundary", 10.0),
				jobDurationRecord("Floor", 5.0),
			},
		},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "job gate: timeout-minutes 45 is below 1.25 x measured duration 44.8m (cancel risk)") {
		t.Errorf("44.8/45 cancel risk must warn; output:\n%s", out)
	}
	if !strings.Contains(out, "job runaway: timeout-minutes 300 is above max(30, 4 x 10.0m) bound of 40m (runaway)") {
		t.Errorf("runaway must warn; output:\n%s", out)
	}
	if strings.Contains(out, "job boundary") {
		t.Errorf("40 minutes boundary must pass; output:\n%s", out)
	}
	if strings.Contains(out, "job floor") {
		t.Errorf("30 minutes floor boundary must pass; output:\n%s", out)
	}
}

// Path naming: offline run prints static-only line with its reason; fake reader run prints measured line with counts.
func TestJobTimeout_PathNaming(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "a.yml", "on: push\njobs:\n  j1:\n    timeout-minutes: 10\n    steps:\n      - run: echo 1\n")
	writeWorkflow(t, root, "b.yml", "on: push\njobs:\n  j2:\n    timeout-minutes: 10\n    steps:\n      - run: echo 2\n")
	offlineOut, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("offline error: %v", err)
	}
	if !strings.Contains(offlineOut, "[INFO] job-timeout: static path only (--offline)") {
		t.Errorf("offline output must name static path reason; got:\n%s", offlineOut)
	}
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"a.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 10, Conclusion: "success"}}},
			"b.yml": {Known: true, Completed: []WorkflowRunRecord{}},
		},
		jobs: map[int64][]WorkflowJobRecord{
			10: {jobDurationRecord("j1", 8.0)},
		},
	}
	measuredOut, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("measured error: %v", err)
	}
	want := "[INFO] job-timeout: measured path for 1 jobs (forge runs of main), static path for 1 jobs (no successful run)"
	if !strings.Contains(measuredOut, want) {
		t.Errorf("measured output must name counts; want %q, got:\n%s", want, measuredOut)
	}
}

// A dated exception silences one finding; an expired one does not; a stale one warns.
func TestJobTimeout_Exceptions(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "fail.yml", "on: push\njobs:\n  j:\n    timeout-minutes: 300\n    steps:\n      - run: echo 1\n")
	writeWorkflow(t, root, "clean.yml", "on: push\njobs:\n  j:\n    timeout-minutes: 30\n    steps:\n      - run: echo 2\n")
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	liveEx := []config.Exception{{
		Rule: config.ExceptionRuleJobTimeout, Path: ".github/workflows/fail.yml", Reason: "waived", Category: "A", Expires: "2026-12-31",
	}}
	outLive, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: today, Exceptions: liveEx, OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(outLive, "[PASS] job-timeout: .github/workflows/fail.yml excepted until 2026-12-31: waived") ||
		strings.Contains(outLive, "[WARN] job-timeout: .github/workflows/fail.yml") {
		t.Errorf("dated exception must silence finding; got:\n%s", outLive)
	}
	expiredEx := []config.Exception{{
		Rule: config.ExceptionRuleJobTimeout, Path: ".github/workflows/fail.yml", Reason: "waived", Expires: "2026-10-01",
	}}
	outExp, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: today, Exceptions: expiredEx, OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(outExp, "[WARN] job-timeout: .github/workflows/fail.yml:4 job j: timeout-minutes 300 is above 120m bound") {
		t.Errorf("expired exception must not silence finding; got:\n%s", outExp)
	}
	staleEx := []config.Exception{{
		Rule: config.ExceptionRuleJobTimeout, Path: ".github/workflows/clean.yml", Reason: "waived", Category: "A", Expires: "2026-12-31",
	}}
	outStale, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: today, Exceptions: staleEx, OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(outStale, "[WARN] job-timeout: the exceptions entry (rule job-timeout, .github/workflows/clean.yml) excuses no finding") {
		t.Errorf("stale exception must warn; got:\n%s", outStale)
	}
}

// beforeFailFrom is the last warn-only day, so tests of the other rules do not change verdict
// when the wall clock passes JobTimeoutFailFrom.
func beforeFailFrom() time.Time {
	return JobTimeoutFailFrom.AddDate(0, 0, -1)
}

// The dated switch: a workflow job without timeout-minutes warns the day before
// JobTimeoutFailFrom and fails the gate on that day and after it; a clean tree passes on the
// switch day (positive), and the warn line names the switch day.
func TestJobTimeout_FailFrom_SwitchBoundary(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "ci.yml", "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 1\n")
	before, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{Root: root, Today: beforeFailFrom(), OfflineReason: "--offline"})
	if err != nil {
		t.Fatalf("the day before the switch must warn, not fail: %v", err)
	}
	if !strings.Contains(before, "[WARN] job-timeout: .github/workflows/ci.yml:3 job build: declares no timeout-minutes") ||
		!strings.Contains(before, "[INFO] job-timeout: findings warn until 2026-11-20 and fail the audit from that day on") {
		t.Errorf("warn-period output must name the finding and the switch day; got:\n%s", before)
	}
	for _, day := range []time.Time{JobTimeoutFailFrom, JobTimeoutFailFrom.AddDate(0, 0, 30)} {
		_, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{Root: root, Today: day, OfflineReason: "--offline"})
		if err == nil || !strings.Contains(err.Error(), "[FAIL] job-timeout: .github/workflows/ci.yml:3 job build: declares no timeout-minutes") {
			t.Errorf("on %s a job without timeout-minutes must fail the gate; got %v", day.Format(time.DateOnly), err)
		}
		if err != nil && !strings.Contains(err.Error(), "[INFO] job-timeout: static path only (--offline)") {
			t.Errorf("the failure must keep the path line; got %v", err)
		}
	}
	clean := t.TempDir()
	writeWorkflow(t, clean, "ci.yml", "on: push\njobs:\n  build:\n    timeout-minutes: 10\n    steps:\n      - run: echo 1\n")
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{Root: clean, Today: JobTimeoutFailFrom, OfflineReason: "--offline"})
	if err != nil || !strings.HasSuffix(out, "[PASS] job-timeout") || strings.Contains(out, "warn until") {
		t.Errorf("a clean tree must pass on the switch day without the warn-period line; out:\n%s\nerr: %v", out, err)
	}
}

// A job whose declared value is above the static ceiling but inside the measured band passes on
// the measured path; the exception that excuses it on the static path (CI without a forge token)
// is in use on both paths, so neither path calls it stale. An exception for a workflow whose
// jobs all stay at or below the static ceiling is still stale on the measured path (negative),
// and after the switch a stale entry fails while a live one still excuses its workflow.
func TestJobTimeout_StaticOnlyExceptionUsedOnBothPaths(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "port.yml", "on: push\njobs:\n  shard:\n    name: Shard\n    timeout-minutes: 163\n    steps:\n      - run: echo 1\n")
	writeWorkflow(t, root, "fast.yml", "on: push\njobs:\n  quick:\n    name: Quick\n    timeout-minutes: 120\n    steps:\n      - run: echo 2\n")
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"port.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 7, Conclusion: "success"}}},
			"fast.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 8, Conclusion: "success"}}},
		},
		jobs: map[int64][]WorkflowJobRecord{7: {jobDurationRecord("Shard", 86.0)}, 8: {jobDurationRecord("Quick", 40.0)}},
	}
	port := config.Exception{Rule: config.ExceptionRuleJobTimeout, Path: ".github/workflows/port.yml", Reason: "measured above the static ceiling", Category: "D", Expires: "2027-03-01"}
	measured, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: JobTimeoutFailFrom, Exceptions: []config.Exception{port}, Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("the static-path exception must not be stale on the measured path: %v", err)
	}
	if !strings.Contains(measured, "[INFO] job-timeout: .github/workflows/port.yml: the exceptions entry (until 2027-03-01) covers jobs above the 120m static bound; the measured path finds none") {
		t.Errorf("the measured path must name why the entry stays; got:\n%s", measured)
	}
	static, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: JobTimeoutFailFrom, Exceptions: []config.Exception{port}, OfflineReason: "--offline",
	})
	if err != nil || !strings.Contains(static, "[PASS] job-timeout: .github/workflows/port.yml excepted until 2027-03-01") {
		t.Errorf("the static path must use the entry; out:\n%s\nerr: %v", static, err)
	}
	fast := port
	fast.Path = ".github/workflows/fast.yml"
	_, err = AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: JobTimeoutFailFrom, Exceptions: []config.Exception{port, fast}, Reader: reader, Branch: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "[FAIL] job-timeout: the exceptions entry (rule job-timeout, .github/workflows/fast.yml) excuses no finding") {
		t.Errorf("an entry for a workflow at or below the static bound must fail as stale after the switch; got %v", err)
	}
}

// Before the switch the gate never fails the audit: a repository with every kind of finding still exits 0 from the gate function.
func TestJobTimeout_WarnOnlyBeforeFailFrom(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n" +
		"  missing:\n    steps:\n      - run: echo 1\n" +
		"  high:\n    timeout-minutes: 330\n    steps:\n      - run: echo 2\n" +
		"  cancel:\n    name: Cancel\n    timeout-minutes: 10\n    steps:\n      - run: echo 3\n"
	writeWorkflow(t, root, "all.yml", content)
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"all.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 99, Conclusion: "success"}}},
		},
		jobs: map[int64][]WorkflowJobRecord{
			99: {jobDurationRecord("Cancel", 20.0)},
		},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("warn-only gate must exit 0, got error: %v", err)
	}
	if !strings.Contains(out, "[PASS] job-timeout") {
		t.Errorf("gate must pass; output:\n%s", out)
	}
}

// Job name mapping matches matrix jobs and prefix before ${{ or ' ('.
func TestJobMatches_MatrixAndPrefix(t *testing.T) {
	cases := []struct {
		target string
		forge  string
		want   bool
	}{
		{"Build (${{ matrix.os }})", "Build (ubuntu-latest)", true},
		{"test", "test (1.22)", true},
		{"lint", "lint", true},
		{"test", "test-other (1.22)", false},
		{"unit", "integration", false},
	}
	for _, tc := range cases {
		if got := jobMatches(tc.target, tc.forge); got != tc.want {
			t.Errorf("jobMatches(%q, %q) = %v, want %v", tc.target, tc.forge, got, tc.want)
		}
	}
}

// When WorkflowRunHistory fails: the job is static and [INFO] line names "could not be read" with error text.
func TestJobTimeout_Unreadable_HistoryFailsJobStatic(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "ci.yml", "on: push\njobs:\n  j:\n    timeout-minutes: 10\n    steps:\n      - run: echo 1\n")
	histErr := errors.New("read run history: HTTP 500")
	reader := &fakeJobTimeoutReader{
		historyErrs: map[string]error{"ci.yml": histErr},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	if !strings.Contains(out, "could not be read") || !strings.Contains(out, histErr.Error()) {
		t.Errorf("expected [INFO] line to name 'could not be read' with error text; got:\n%s", out)
	}
	if !strings.Contains(out, "static path for 1 jobs") {
		t.Errorf("expected static path for 1 jobs; got:\n%s", out)
	}
}

// When WorkflowRunJobs fails for one run while another run succeeds: measured from readable run.
func TestJobTimeout_Unreadable_RunJobsFallbackToReadableRun(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n  gate:\n    name: Standards\n    timeout-minutes: 45\n    steps:\n      - run: echo 1\n"
	writeWorkflow(t, root, "ci.yml", content)
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"ci.yml": {Known: true, Completed: []WorkflowRunRecord{
				{ID: 101, Conclusion: "success"},
				{ID: 102, Conclusion: "success"},
			}},
		},
		jobsErrs: map[int64]error{101: errors.New("HTTP 502")},
		jobs: map[int64][]WorkflowJobRecord{
			102: {jobDurationRecord("Standards", 44.8)},
		},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	if !strings.Contains(out, "measured path for 1 jobs") {
		t.Errorf("expected measured path from readable run; got:\n%s", out)
	}
	if !strings.Contains(out, "job gate: timeout-minutes 45 is below 1.25 x measured duration 44.8m (cancel risk)") {
		t.Errorf("job must be measured from readable run (44.8m); output:\n%s", out)
	}
}

// A run with ID 0 is skipped and counted unreadable; no jobs call is made for it (fake records calls).
func TestJobTimeout_Unreadable_ZeroRunIDSkipped(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "ci.yml", "on: push\njobs:\n  j:\n    timeout-minutes: 10\n    steps:\n      - run: echo 1\n")
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"ci.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 0, Number: 42, Conclusion: "success"}}},
		},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	if len(reader.jobsCalls) != 0 {
		t.Errorf("no jobs call must be made for run with ID 0; got calls: %v", reader.jobsCalls)
	}
	if !strings.Contains(out, "whose runs could not be read") {
		t.Errorf("expected job counted as unreadable; output:\n%s", out)
	}
	if !strings.Contains(out, "run without ID") {
		t.Errorf("expected run without ID named in line; output:\n%s", out)
	}
}

// When WorkflowRunJobs fails for all runs: counted unreadable and names read run jobs error.
func TestJobTimeout_Unreadable_RunJobsFailsAllRuns(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "ci.yml", "on: push\njobs:\n  j:\n    timeout-minutes: 10\n    steps:\n      - run: echo 1\n")
	reader := &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			"ci.yml": {Known: true, Completed: []WorkflowRunRecord{{ID: 99, Conclusion: "success"}}},
		},
		jobsErrs: map[int64]error{99: errors.New("HTTP 502")},
	}
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: beforeFailFrom(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	want := "static path for 1 jobs: 0 without a successful run, 1 whose runs could not be read (read run jobs: HTTP 502)"
	if !strings.Contains(out, want) {
		t.Errorf("expected unreadable line with error; want %q, got:\n%s", want, out)
	}
}
