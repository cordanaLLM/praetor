// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
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
		Root: root, Today: time.Now(), OfflineReason: "--offline",
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
		Root: root, Today: time.Now(), OfflineReason: "--offline",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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
		Root: root, Today: time.Now(), OfflineReason: "--offline",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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

// The gate never fails the audit (warn-only): a repository with every kind of finding still exits 0 from the gate function.
func TestJobTimeout_WarnOnlyNeverFails(t *testing.T) {
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
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
		Root: root, Today: time.Now(), Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	want := "static path for 1 jobs: 0 without a successful run, 1 whose runs could not be read (read run jobs: HTTP 502)"
	if !strings.Contains(out, want) {
		t.Errorf("expected unreadable line with error; want %q, got:\n%s", want, out)
	}
}

// liveJobTimeoutException builds a live job-timeout exception entry for workflow rel.
func liveJobTimeoutException(rel, reason string) []config.Exception {
	return []config.Exception{{
		Rule:     config.ExceptionRuleJobTimeout,
		Path:     rel,
		Reason:   reason,
		Category: "A",
		Expires:  "2026-12-31",
	}}
}

// singleRunJobTimeoutReader builds a fake ActionsReader with one successful run on workflow and jobs.
func singleRunJobTimeoutReader(workflow string, jobs ...WorkflowJobRecord) *fakeJobTimeoutReader {
	return &fakeJobTimeoutReader{
		histories: map[string]WorkflowRunHistory{
			workflow: {Known: true, Completed: []WorkflowRunRecord{{ID: 1, Conclusion: "success"}}},
		},
		jobs: map[int64][]WorkflowJobRecord{1: jobs},
	}
}

// assertExcusedJobTimeoutPass writes workflow name, runs AuditJobTimeouts under a live exception,
// and checks that the [PASS] line carries wantPass with no [WARN] lines.
func assertExcusedJobTimeoutPass(t *testing.T, name, content string, reader ActionsReader, offlineReason, reason, wantPass string) {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, name, content)
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	opts := JobTimeoutOptions{
		Root:          root,
		Today:         today,
		Exceptions:    liveJobTimeoutException(".github/workflows/"+name, reason),
		Reader:        reader,
		OfflineReason: offlineReason,
		Branch:        "main",
	}
	out, err := AuditJobTimeouts(t.Context(), opts)
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	if !strings.Contains(out, wantPass) {
		t.Errorf("expected excused [PASS] line %q; got:\n%s", wantPass, out)
	}
	if strings.Contains(out, "[WARN]") {
		t.Errorf("live exception must emit no [WARN] line; got:\n%s", out)
	}
}

// An excused workflow whose only finding is a cancel risk names "excuses job <id> cancel-risk".
func TestJobTimeout_ExcusedKind_CancelRisk(t *testing.T) {
	content := "on: push\njobs:\n  gate:\n    name: Standards\n    timeout-minutes: 45\n    steps:\n      - run: echo gate\n"
	reader := singleRunJobTimeoutReader("ci.yml", jobDurationRecord("Standards", 44.8))
	want := "[PASS] job-timeout: .github/workflows/ci.yml excepted until 2026-12-31: waived; excuses job gate cancel-risk"
	assertExcusedJobTimeoutPass(t, "ci.yml", content, reader, "", "waived", want)
}

// An excused workflow whose only finding is a runaway names "excuses job <id> runaway".
func TestJobTimeout_ExcusedKind_Runaway(t *testing.T) {
	content := "on: push\njobs:\n  slow:\n    name: Slow\n    timeout-minutes: 300\n    steps:\n      - run: echo slow\n"
	reader := singleRunJobTimeoutReader("ci.yml", jobDurationRecord("Slow", 10.0))
	want := "[PASS] job-timeout: .github/workflows/ci.yml excepted until 2026-12-31: waived; excuses job slow runaway"
	assertExcusedJobTimeoutPass(t, "ci.yml", content, reader, "", "waived", want)
}

// An excused workflow with two findings of different kinds lists both "job <id> <kind>" entries in finding order.
func TestJobTimeout_ExcusedKind_MultipleFindingsOrder(t *testing.T) {
	measured := "on: push\njobs:\n" +
		"  alpha:\n    name: Alpha\n    timeout-minutes: 45\n    steps:\n      - run: echo alpha\n" +
		"  beta:\n    name: Beta\n    timeout-minutes: 300\n    steps:\n      - run: echo beta\n"
	reader := singleRunJobTimeoutReader("ci.yml", jobDurationRecord("Alpha", 44.8), jobDurationRecord("Beta", 10.0))
	wantMeasured := "[PASS] job-timeout: .github/workflows/ci.yml excepted until 2026-12-31: waived; excuses job alpha cancel-risk, job beta runaway"
	assertExcusedJobTimeoutPass(t, "ci.yml", measured, reader, "", "waived", wantMeasured)

	mixed := "on: push\njobs:\n" +
		"  alpha:\n    name: Alpha\n    timeout-minutes: 300\n    steps:\n      - run: echo alpha\n" +
		"  beta:\n    name: Beta\n    timeout-minutes: 180\n    steps:\n      - run: echo beta\n"
	mixedReader := singleRunJobTimeoutReader("mixed.yml", jobDurationRecord("Alpha", 10.0))
	wantMixed := "[PASS] job-timeout: .github/workflows/mixed.yml excepted until 2026-12-31: waived; excuses job alpha runaway, job beta static-ceiling"
	assertExcusedJobTimeoutPass(t, "mixed.yml", mixed, mixedReader, "", "waived", wantMixed)
}

// Static path above the static ceiling under a live exception names "static-ceiling".
func TestJobTimeout_ExcusedKind_StaticCeiling(t *testing.T) {
	content := "on: push\njobs:\n  heavy:\n    timeout-minutes: 180\n    steps:\n      - run: echo heavy\n"
	want := "[PASS] job-timeout: .github/workflows/static.yml excepted until 2026-12-31: waived; excuses job heavy static-ceiling"
	assertExcusedJobTimeoutPass(t, "static.yml", content, nil, "--offline", "waived", want)
}

// Negative: an unexcused workflow's [WARN] line carries no "excuses", and an expired exception keeps today's [WARN] lines.
func TestJobTimeout_ExcusedKind_Negative_UnexcusedAndExpired(t *testing.T) {
	root := t.TempDir()
	content := "on: push\njobs:\n  gate:\n    name: Standards\n    timeout-minutes: 45\n    steps:\n      - run: echo gate\n"
	writeWorkflow(t, root, "ci.yml", content)
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	reader := singleRunJobTimeoutReader("ci.yml", jobDurationRecord("Standards", 44.8))

	outUnexcused, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: today, Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("unexcused AuditJobTimeouts failed: %v", err)
	}
	wantWarn := "[WARN] job-timeout: .github/workflows/ci.yml:5 job gate: timeout-minutes 45 is below 1.25 x measured duration 44.8m (cancel risk)"
	if !strings.Contains(outUnexcused, wantWarn) || strings.Contains(outUnexcused, "excuses") {
		t.Errorf("unexcused workflow must emit [WARN] without excuses; got:\n%s", outUnexcused)
	}

	expired := []config.Exception{{
		Rule: config.ExceptionRuleJobTimeout, Path: ".github/workflows/ci.yml", Reason: "waived", Expires: "2026-10-01",
	}}
	outExpired, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root: root, Today: today, Exceptions: expired, Reader: reader, Branch: "main",
	})
	if err != nil {
		t.Fatalf("expired AuditJobTimeouts failed: %v", err)
	}
	if !strings.Contains(outExpired, wantWarn) || strings.Contains(outExpired, "excuses") {
		t.Errorf("expired exception must keep [WARN] line without excuses; got:\n%s", outExpired)
	}
}

// An excused workflow whose only finding is a missing timeout-minutes names "missing-timeout".
func TestJobTimeout_ExcusedKind_MissingTimeout(t *testing.T) {
	content := "on: push\njobs:\n  bare:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo bare\n"
	want := "[PASS] job-timeout: .github/workflows/missing.yml excepted until 2026-12-31: waived; excuses job bare missing-timeout"
	assertExcusedJobTimeoutPass(t, "missing.yml", content, nil, "--offline", "waived", want)
}

// An excused workflow whose only finding is an unreadable timeout-minutes names "unreadable-timeout".
func TestJobTimeout_ExcusedKind_UnreadableTimeout(t *testing.T) {
	content := "on: push\njobs:\n  bad:\n    timeout-minutes: five\n    steps:\n      - run: echo bad\n"
	want := "[PASS] job-timeout: .github/workflows/unreadable.yml excepted until 2026-12-31: waived; excuses job bad unreadable-timeout"
	assertExcusedJobTimeoutPass(t, "unreadable.yml", content, nil, "--offline", "waived", want)
}

// Accepted job IDs retain every sanitized character, including multibyte runes at the input limit.
func TestAuditJobID_Boundary_PreservesCompleteSanitizedID(t *testing.T) {
	prefix := strings.Repeat("a", contextopt.MaxSourceBytes-len("界\x00"))
	raw := prefix + "界\x00"
	if got := auditJobID(raw); got != prefix+"界." {
		t.Fatalf("sanitized boundary ID changed: got %d bytes, want %d", len(got), len(raw))
	}
	if got := auditJobID(""); got != "" {
		t.Fatalf("empty ID = %q, want empty", got)
	}
	if got := auditJobID("build\n界\u0085done"); got != "build.界.done" {
		t.Fatalf("sanitized ID = %q, want %q", got, "build.界.done")
	}
}

// IDs beyond the workflow input bound produce a diagnostic marker in both audit paths.
func TestAuditJobID_Negative_RejectsOversizedInput(t *testing.T) {
	raw := strings.Repeat("a", contextopt.MaxSourceBytes+1)
	const want = "[oversized job ID]"
	if got := auditJobID(raw); got != want {
		t.Fatalf("oversized ID: got %d bytes, want %q", len(got), want)
	}
	finding := jobTimeoutFinding{Workflow: ".github/workflows/ci.yml", JobID: raw, Kind: jobTimeoutKindMissing}
	if got := formatExcusedFindings([]jobTimeoutFinding{finding}); got != "job "+want+" missing-timeout" {
		t.Fatalf("excused oversized ID: got %d bytes", len(got))
	}
	lines := applyExceptions([]jobTimeoutFinding{finding}, nil, time.Time{})
	if len(lines) != 1 || !strings.Contains(lines[0], "job "+want+":") {
		t.Fatal("warning path did not mark the oversized ID")
	}
}

// formatExcusedFindings must not let terminal control characters in a job key forge audit-log lines.
func TestFormatExcusedFindings_SanitizesControlCharJobID(t *testing.T) {
	rawID := "gate\x1bforged"
	got := formatExcusedFindings([]jobTimeoutFinding{
		{JobID: rawID, Kind: jobTimeoutKindCancelRisk},
	})
	if strings.Contains(got, "\x1b") {
		t.Fatalf("control character must not reach formatted output; got %q", got)
	}
	if !strings.Contains(got, "job gate.forged cancel-risk") {
		t.Fatalf("expected sanitized job id in output; got %q", got)
	}
}

// formatExcusedFindings stops after ghworkflow.MaxJobsPerFile entries (HISS-02 scalar cap).
func TestFormatExcusedFindings_Boundary_MaxJobsPerFile(t *testing.T) {
	var findings []jobTimeoutFinding
	for i := 0; i < ghworkflow.MaxJobsPerFile+2; i++ {
		findings = append(findings, jobTimeoutFinding{
			JobID: fmt.Sprintf("j%d", i), Kind: jobTimeoutKindStaticCeiling,
		})
	}
	got := formatExcusedFindings(findings)
	parts := strings.Split(got, ", ")
	if len(parts) != ghworkflow.MaxJobsPerFile {
		t.Fatalf("expected %d formatted entries, got %d: %q", ghworkflow.MaxJobsPerFile, len(parts), got)
	}
	if !strings.HasSuffix(parts[len(parts)-1], "static-ceiling") {
		t.Fatalf("last entry must be j%d; got %q", ghworkflow.MaxJobsPerFile-1, parts[len(parts)-1])
	}
}

// Boundary: a workflow with an exception but zero findings keeps today's unused-entry [WARN] line and prints no excused-job text.
func TestJobTimeout_ExcusedKind_Boundary_UnusedException(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "clean.yml", "on: push\njobs:\n  ok:\n    timeout-minutes: 30\n    steps:\n      - run: echo ok\n")
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	out, err := AuditJobTimeouts(t.Context(), JobTimeoutOptions{
		Root:          root,
		Today:         today,
		Exceptions:    liveJobTimeoutException(".github/workflows/clean.yml", "waived"),
		OfflineReason: "--offline",
	})
	if err != nil {
		t.Fatalf("AuditJobTimeouts failed: %v", err)
	}
	wantUnused := "[WARN] job-timeout: the exceptions entry (rule job-timeout, .github/workflows/clean.yml) excuses no finding, because the workflow it names has none; remove it."
	if !strings.Contains(out, wantUnused) {
		t.Errorf("unused exception must keep today's [WARN] line; got:\n%s", out)
	}
	if strings.Contains(out, "; excuses ") || strings.Contains(out, "excuses job ") || strings.Contains(out, "excepted until") {
		t.Errorf("zero-finding workflow must print no excused-job text; got:\n%s", out)
	}
}
