// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

func TestReview8UnreadableBaselineRecordsError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, baselineFile), "{")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	err := reconcileBaseline(t.Context(), s)
	if err == nil || len(s.report.Errors) == 0 {
		t.Fatalf("unreadable baseline not in report errors: err=%v report=%+v", err, s.report)
	}
}

func TestReview8MigrationActions(t *testing.T) {
	for _, dry := range []bool{false, true} {
		for _, record := range []bool{false, true} {
			t.Run(fmt.Sprintf("dry=%t/record=%t", dry, record), func(t *testing.T) { checkReview8MigrationActions(t, dry, record) })
		}
	}
}

func checkReview8MigrationActions(t *testing.T, dry, record bool) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, baselineFile), `{"version":1,"infractions":[{"rule_id":"HISS-07"},{"rule_id":"README-01"}]}`)
	s := &adoptSession{repoPath: root, opts: AdoptOptions{DryRun: dry, RecordBaseline: record}, report: &AdoptReport{DebtBreakdown: make(map[string]int)}}
	if err := reconcileBaseline(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.report.ReconciledFiles) != 1 || s.report.ReconciledFiles[0] != baselineFile {
		t.Errorf("duplicate or misclassified reconciliation: %v", s.report.ReconciledFiles)
	}
	if len(s.report.CreatedFiles) != 1 || s.report.CreatedFiles[0] != ".readme-baseline.json" {
		t.Errorf("migrated README missing from Created: %v", s.report.CreatedFiles)
	}
	detail := ""
	for _, d := range s.report.ActionDetails {
		if d.Path == baselineFile {
			detail += d.Details
		}
	}
	want := map[bool]string{false: "verified present", true: "HISS-13 ratchet passes"}[record]
	if !strings.Contains(detail, "README entries") || !strings.Contains(detail, want) {
		t.Errorf("migration or verification detail lost: %s", detail)
	}
}

func TestReview8ReadmeClassRemedy(t *testing.T) {
	b := &baseline.Baseline{Infractions: []baseline.Infraction{{RuleID: "HISS-07"}}}
	err := b.ValidateClass(".readme-baseline.json", baseline.READMEClass)
	if err == nil {
		t.Fatal("foreign class accepted")
	}
	if !strings.Contains(err.Error(), "baseline --record") || !strings.Contains(err.Error(), ".standards-baseline.json") || strings.Contains(err.Error(), "praetorctl adopt") {
		t.Errorf("wrong README class remedy: %v", err)
	}
}
