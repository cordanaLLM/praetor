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
	"github.com/cordanaLLM/praetor/internal/testsupport"
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
	rec, err := baseline.Record(&baseline.Baseline{Absent: true}, ToBaseline(findings), baseline.RecordOptions{Repository: "fixture/repo", AllowIncrease: true, Rationale: "initial README fixture"})
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
	if err != nil || !strings.Contains(line, "[PASS] README presentation: 1 active finding(s) within 1 recorded in .readme-baseline.json.") {
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
	zero := 0
	if _, err := Audit(t.Context(), Options{Root: tempDir, Baseline: rec, MaxStale: &zero}); err == nil || !strings.Contains(err.Error(), "--max-stale-baseline-entries=0 allows at most 0") {
		t.Fatalf("expected stale baseline entry to fail MaxStale=0, got err=%v", err)
	}
}

func assertReportAndEnforceReadFailure(t *testing.T, dir, wantMsg string) {
	t.Helper()
	line, err := Audit(t.Context(), Options{Root: dir})
	if err != nil || !strings.Contains(line, "[REPORT] README-00") || !strings.Contains(line, wantMsg) {
		t.Fatalf("report mode on unreadable/invalid README: want %q, err=%v line=%q", wantMsg, err, line)
	}
	if _, err := Audit(t.Context(), Options{Root: dir, Enforce: true}); err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("enforce mode on unreadable/invalid README: want %q, got err=%v", wantMsg, err)
	}
}

func TestAudit_Boundary_DirectoryUnreadableAndOversizedReportVsEnforce(t *testing.T) {
	t.Run("directory README", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "README.md"), 0o750); err != nil {
			t.Fatal(err)
		}
		assertReportAndEnforceReadFailure(t, dir, "README.md is not a regular file")
	})

	t.Run("oversized README", func(t *testing.T) {
		dir := t.TempDir()
		writeTestReadme(t, dir, strings.Repeat("a", MaxFileBytes+1))
		assertReportAndEnforceReadFailure(t, dir, "README.md exceeds maximum size")
	})

	t.Run("unreadable README", func(t *testing.T) {
		testsupport.SkipIfFileModeUnenforced(t)
		dir := t.TempDir()
		p := filepath.Join(dir, "README.md")
		writeTestReadme(t, dir, "# x\n")
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(p, 0o600); err != nil {
				t.Log(err)
			}
		})
		assertReportAndEnforceReadFailure(t, dir, "README.md cannot be read (permission denied)")
	})
}

func TestAudit_Boundary_RatchetDistinguishesMessagesAndIgnoresLineShifts(t *testing.T) {
	dir := t.TempDir()
	base := "# proj\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n\n"
	writeTestReadme(t, dir, base+"## Foo\n\n## Foo\n")
	rec := recordTestReadmeBaseline(t, dir)

	// Shifting the same duplicate-heading violation down by lines still passes the ratchet.
	writeTestReadme(t, dir, base+"\n\n\n## Foo\n\n## Foo\n")
	if _, err := Audit(t.Context(), Options{Root: dir, Baseline: rec}); err != nil {
		t.Fatalf("same violation shifted by line numbers should pass ratchet: %v", err)
	}

	// Swapping the duplicate-heading README-05 for an out-of-order README-05 fails the ratchet.
	writeTestReadme(t, dir, base+"## Contribute\n\n## Get started\n")
	if _, err := Audit(t.Context(), Options{Root: dir, Baseline: rec}); err == nil {
		t.Fatal("different README-05 violation message should fail ratchet")
	}
}

func TestAudit_Positive_GovernanceBlockAtTopEmitsInfoAndZeroCounted(t *testing.T) {
	dir := t.TempDir()
	content := "# proj\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n\n" +
		"<!-- praetor:readme-governance:start -->\ngoverned\n<!-- praetor:readme-governance:end -->\n\n## 🚀 Get started\n"
	writeTestReadme(t, dir, content)
	line, err := Audit(t.Context(), Options{Root: dir, Enforce: true})
	if err != nil {
		t.Fatalf("governance block at top should pass enforce with info line, got err=%v", err)
	}
	if !strings.Contains(line, "[PASS] README presentation:") || !strings.Contains(line, "[INFO] README-05") {
		t.Fatalf("expected [PASS] and [INFO] README-05, got:\n%s", line)
	}
}

func checkSingleFindingToBaseline(t *testing.T, root, doc string) ([]Finding, baseline.Infraction) {
	t.Helper()
	writeTestReadme(t, root, doc)
	findings, err := CheckFile(t.Context(), root, FileName)
	if err != nil || len(findings) != 1 {
		t.Fatalf("%s CheckFile: findings=%v err=%v", root, findings, err)
	}
	return findings, ToBaseline(findings)[0]
}

func TestAudit_Negative_SameReadmeUnderTwoRootsEqualAnchors(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	doc := "# proj\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n\n![Missing](docs/assets/missing.svg)\n"
	findingsA, infA := checkSingleFindingToBaseline(t, rootA, doc)
	_, infB := checkSingleFindingToBaseline(t, rootB, doc)
	if infA != infB || strings.Contains(infA.Message, rootA) {
		t.Fatalf("anchors/messages differ or leaked root:\nrootA: %+v\nrootB: %+v", infA, infB)
	}
	recA, err := baseline.Record(&baseline.Baseline{Absent: true}, ToBaseline(findingsA), baseline.RecordOptions{Repository: "fixture/repo", AllowIncrease: true, Rationale: "initial README fixture"})
	if err != nil {
		t.Fatalf("RecordBaseline: %v", err)
	}
	line, err := Audit(t.Context(), Options{Root: rootB, Baseline: recA, Enforce: true})
	if err != nil || strings.Contains(line, "measured, not enforced") || !strings.Contains(line, "(baselined)") {
		t.Fatalf("Audit on second root: err=%v line=\n%s", err, line)
	}
}

func TestAudit_Negative_ScanBaselineFindingsReportOnlyAndRefusalText(t *testing.T) {
	dir := t.TempDir()
	writeTestReadme(t, dir, "# widgets\n")
	zeroBase := &baseline.Baseline{Version: 1, TotalInfractions: 0}

	findings, err := ScanBaselineFindings(t.Context(), dir, nil, false)
	if err != nil || len(findings) != 0 {
		t.Fatalf("expected 0 baseline findings when not enforced and no prior README entries, got findings=%v err=%v", findings, err)
	}
	enforcedFindings, err := ScanBaselineFindings(t.Context(), dir, zeroBase, true)
	if err != nil || len(enforcedFindings) == 0 {
		t.Fatalf("expected findings when enforce=true, got findings=%v err=%v", enforcedFindings, err)
	}
	_, err = baseline.Record(zeroBase, ToBaseline(enforcedFindings), baseline.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "baseline: total infractions increased") {
		t.Fatalf("expected RecordBaseline refusal to name README presentation findings apart from HISS, got: %v", err)
	}
}
