// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

func TestAudit_Positive_CompliantReadme(t *testing.T) {
	tempDir := t.TempDir()
	exemplarData, err := os.ReadFile(filepath.Join("testdata", "README-vmafx-2929.md"))
	if err != nil {
		t.Fatalf("read exemplar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), exemplarData, 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	line, err := Audit(t.Context(), Options{Root: tempDir})
	if err != nil {
		t.Fatalf("Audit unexpected error: %v", err)
	}
	if !strings.HasPrefix(line, "[PASS] README presentation:") {
		t.Errorf("expected [PASS], got: %s", line)
	}
}

func TestAudit_Positive_DefaultWithoutBaselineOrSwitchReportsWithoutFailing(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# widgets\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	line, err := Audit(t.Context(), Options{Root: tempDir})
	if err != nil {
		t.Fatalf("expected nil error in default report mode, got: %v", err)
	}
	if !strings.Contains(line, "[REPORT] README presentation measured, not enforced:") {
		t.Errorf("expected [REPORT] summary line, got:\n%s", line)
	}
	if !strings.Contains(line, "[REPORT] README-01 README.md:1 missing style=for-the-badge badge block under header (report: measured, not enforced)") {
		t.Errorf("expected README-01 [REPORT] line, got:\n%s", line)
	}
}

func TestAudit_Positive_AbsentReadmeReportedNeverInfoPass(t *testing.T) {
	tempDir := t.TempDir()

	line, err := Audit(t.Context(), Options{Root: tempDir})
	if err != nil {
		t.Fatalf("expected nil error in default report mode for absent README, got: %v", err)
	}
	if strings.Contains(line, "[INFO]") || strings.Contains(line, "[PASS]") {
		t.Errorf("absent README must never emit [INFO] or [PASS], got:\n%s", line)
	}
	if !strings.Contains(line, "[REPORT] README-00 README.md:1 README.md is absent (report: measured, not enforced)") {
		t.Errorf("expected README-00 [REPORT] line, got:\n%s", line)
	}
}

func TestAudit_Negative_EnforceSwitchFailsOnFindings(t *testing.T) {
	tempDir := t.TempDir()
	badContent := "# My Project\n\nSome unbolded text.\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n"
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(badContent), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	_, err := Audit(t.Context(), Options{
		Root:    tempDir,
		Enforce: true,
	})
	if err == nil {
		t.Fatal("Audit expected error when Enforce is true, got nil")
	}
	if !strings.Contains(err.Error(), "[FAIL] README presentation:") {
		t.Errorf("expected [FAIL] in error, got: %s", err.Error())
	}
}

func writeTestReadme(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
}

func recordTestReadmeBaseline(t *testing.T, dir string) *baseline.Baseline {
	t.Helper()
	findings, err := CheckFile(t.Context(), dir, FileName)
	if err != nil {
		t.Fatalf("CheckFile: %v", err)
	}
	rec, err := RecordBaseline(nil, findings, baseline.RecordOptions{Repository: "fixture/repo"})
	if err != nil {
		t.Fatalf("RecordBaseline: %v", err)
	}
	return rec
}

func TestAudit_Boundary_BaselineRatchetPassesRecordedFailsAboveReportsStale(t *testing.T) {
	tempDir := t.TempDir()
	writeTestReadme(t, tempDir, "# widgets\n")
	rec := recordTestReadmeBaseline(t, tempDir)

	// 1. Recorded findings pass the baseline ratchet.
	line, err := Audit(t.Context(), Options{Root: tempDir, Baseline: rec})
	if err != nil || !strings.Contains(line, "[PASS] README presentation: 1 active finding(s) within 1 recorded in baseline.") {
		t.Fatalf("expected recorded findings to pass ratchet, got line=%q err=%v", line, err)
	}

	// 2. Findings above baseline fail the ratchet.
	writeTestReadme(t, tempDir, "# widgets\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n")
	if _, err := Audit(t.Context(), Options{Root: tempDir, Baseline: rec}); err == nil {
		t.Fatal("expected findings above baseline to fail ratchet, got nil")
	}

	// 3. Fixing README reports stale baseline entries.
	exemplarData, err := os.ReadFile(filepath.Join("testdata", "README-vmafx-2929.md"))
	if err != nil {
		t.Fatalf("read exemplar: %v", err)
	}
	writeTestReadme(t, tempDir, string(exemplarData))
	staleLine, err := Audit(t.Context(), Options{Root: tempDir, Baseline: rec})
	if err != nil || !strings.Contains(staleLine, "[WARN] README presentation: 1 baseline entry matches nothing in the tree") {
		t.Fatalf("expected stale baseline warning, got line=%q err=%v", staleLine, err)
	}
}

func TestWithoutReadmeBaseline_FiltersOnlyReadmeRules(t *testing.T) {
	b := &baseline.Baseline{
		Version:          1,
		TotalInfractions: 2,
		Infractions: []baseline.Infraction{
			{RuleID: "HISS-01", FilePath: "main.go", LineNumber: 10},
			{RuleID: RuleIDBadgeBlock, FilePath: FileName, LineNumber: 1},
		},
	}
	filtered := WithoutReadmeBaseline(b)
	if filtered.TotalInfractions != 1 || len(filtered.Infractions) != 1 || filtered.Infractions[0].RuleID != "HISS-01" {
		t.Fatalf("unexpected filtered baseline: %+v", filtered)
	}
}
