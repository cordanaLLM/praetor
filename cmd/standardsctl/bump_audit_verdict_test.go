// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/bump"
)

// Positive, negative and boundary (#613): the exit follows report.Passed. A passing report
// exits 0 even with drift, which lowers only the score; a failed report names its
// deprecation count.
func TestBumpAuditVerdictFollowsPassed(t *testing.T) {
	if err := bumpAuditVerdict(&bump.VersionAuditReport{TotalScanned: 2, UpToDate: 2, ModernizationScore: 100, Passed: true}); err != nil {
		t.Fatalf("passing report: %v, want nil", err)
	}
	drift := &bump.VersionAuditReport{TotalScanned: 2, UpToDate: 1, ModernizationScore: 50, Passed: true}
	if err := bumpAuditVerdict(drift); err != nil {
		t.Fatalf("drift-only passing report: %v, want nil", err)
	}
	failed := &bump.VersionAuditReport{TotalScanned: 3, UpToDate: 1, ModernizationScore: 33.3, Passed: false,
		Deprecations: []bump.DeprecationWarning{{Component: "actions/upload-artifact@v4", Kind: "runner-runtime-deprecated"}}}
	err := bumpAuditVerdict(failed)
	if err == nil || !strings.Contains(err.Error(), "bump audit failed: 1 deprecation(s); modernization score 33.3%") {
		t.Fatalf("failed report: %v, want the deprecation count and score", err)
	}
}

// Negative (#613): bump audit prints the report, then exits non-zero when the report it
// built failed. PATH holds no tool, so the probed toolchains add their own deprecations on
// every host and the verdict does not depend on what is installed.
func TestRunBumpAuditExitsNonZeroOnFailedReport(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".github/workflows/ci.yml",
		"name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/upload-artifact@v4\n")
	text, err := captureStdout(t, func() error {
		return runBumpAudit(t.Context(), []string{"--path", dir})
	})
	if err == nil || !strings.Contains(err.Error(), "bump audit failed:") {
		t.Fatalf("bump audit err = %v, want the failed verdict\n%s", err, text)
	}
	for _, want := range []string{"Passed:              false", "[DEPRECATED]", "Deprecation Warnings & Breaking Advisories:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bump audit output lacks %q:\n%s", want, text)
		}
	}
}
