// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"os"
	"path/filepath"
	"testing"
)

// workflowRepo writes a repository whose only manifest is one workflow using each action.
func workflowRepo(t *testing.T, uses ...string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	workflow := "name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n"
	for _, use := range uses {
		workflow += "      - uses: " + use + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

// auditWorkflow audits a workflow-only repository with every probed toolchain present, so
// only the actions decide the counts.
func auditWorkflow(t *testing.T, uses ...string) *VersionAuditReport {
	t.Helper()
	repo := workflowRepo(t, uses...)
	t.Setenv("PATH", toolchainBin(t))
	report, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// Positive: a workflow whose actions are all current counts every row up to date, scores
// 100 and passes.
func TestAuditActions_Positive_AllCurrentActionsScoreFull(t *testing.T) {
	report := auditWorkflow(t, "actions/checkout@v7", "actions/setup-go@v7")
	if report.TotalScanned != 2 || report.UpToDate != 2 || report.ModernizationScore != 100 || !report.Passed {
		t.Fatalf("scanned=%d up_to_date=%d score=%.1f passed=%v, want 2, 2, 100, passed",
			report.TotalScanned, report.UpToDate, report.ModernizationScore, report.Passed)
	}
}

// Negative (#613): each [DRIFT] row lowers Up To Date and the score. Drift is no
// deprecation, so a drift-only report still passes.
func TestAuditActions_Negative_DriftLowersUpToDate(t *testing.T) {
	report := auditWorkflow(t, "actions/checkout@v5", "actions/setup-go@v7")
	if report.TotalScanned != 2 || report.UpToDate != 1 || report.ModernizationScore != 50 {
		t.Fatalf("scanned=%d up_to_date=%d score=%.1f, want 2, 1, 50",
			report.TotalScanned, report.UpToDate, report.ModernizationScore)
	}
	if !report.Passed || len(report.Deprecations) != 0 {
		t.Fatalf("passed=%v deprecations=%+v, want a passing report without deprecations", report.Passed, report.Deprecations)
	}
}

// Negative (#613, the reported workflow): one drifted, one current and one deprecated action
// count one row up to date, not two, and the deprecation fails the report.
func TestAuditActions_Negative_DriftAndDeprecationFailReport(t *testing.T) {
	report := auditWorkflow(t, "actions/checkout@v5", "actions/setup-go@v7", "actions/upload-artifact@v4")
	if report.TotalScanned != 3 || report.UpToDate != 1 {
		t.Fatalf("scanned=%d up_to_date=%d, want 3 and 1", report.TotalScanned, report.UpToDate)
	}
	if report.Passed || len(report.Deprecations) != 1 {
		t.Fatalf("passed=%v deprecations=%+v, want a failed report with one deprecation", report.Passed, report.Deprecations)
	}
}

// Boundary: a [DEPRECATED] row is also behind the registry and lowers Up To Date once, not
// once as drift and again as a deprecation.
func TestAuditActions_Boundary_DeprecatedRowCountsOnce(t *testing.T) {
	report := auditWorkflow(t, "actions/checkout@v7", "actions/setup-go@v7", "actions/upload-artifact@v4")
	if report.TotalScanned != 3 || report.UpToDate != 2 || report.Passed {
		t.Fatalf("scanned=%d up_to_date=%d passed=%v, want 3, 2 and failed", report.TotalScanned, report.UpToDate, report.Passed)
	}
}

// Boundary: actionsBehind follows ActionDriftStatus row by row, and no rows count nothing.
func TestActionsBehind_Boundaries(t *testing.T) {
	cases := []struct {
		name    string
		actions []ActionCandidate
		want    int
	}{
		{"none", nil, 0},
		{"current", []ActionCandidate{{UpToDate: true}}, 0},
		{"drift", []ActionCandidate{{UpToDate: false}}, 1},
		{"deprecated behind", []ActionCandidate{{UpToDate: false, Deprecated: true}}, 1},
		{"deprecated current", []ActionCandidate{{UpToDate: true, Deprecated: true}}, 1},
		{"mixed", []ActionCandidate{{UpToDate: true}, {UpToDate: false}, {UpToDate: false, Deprecated: true}}, 2},
	}
	for _, tc := range cases {
		if got := actionsBehind(tc.actions); got != tc.want {
			t.Errorf("%s: actionsBehind = %d, want %d", tc.name, got, tc.want)
		}
	}
}
