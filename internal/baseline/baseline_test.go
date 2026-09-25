package baseline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateRatchet(t *testing.T) {
	b := &Baseline{
		Version:          1,
		TotalInfractions: 2,
		Infractions: []Infraction{
			{RuleID: "HISS-04", FilePath: "legacy/old.go", LineNumber: 10, Fingerprint: "fp1"},
			{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
		},
	}

	current := []Infraction{
		{RuleID: "HISS-04", FilePath: "legacy/old.go", LineNumber: 10, Fingerprint: "fp1"},
		{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
	}
	res := EvaluateRatchet(b, current, []string{"new_feature.go"})
	if !res.Passed {
		t.Fatalf("expected untouched legacy to pass")
	}

	// Refactored clean: debt decreased
	currentRefactored := []Infraction{
		{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
	}
	resDecreased := EvaluateRatchet(b, currentRefactored, []string{"legacy/old.go"})
	if !resDecreased.Passed || resDecreased.CurrentCount >= resDecreased.PreviousCount {
		t.Fatalf("expected debt decrease with refactored file to pass")
	}
}

func TestEvaluateRatchet_NegativeAndBoundary(t *testing.T) {
	b := &Baseline{
		Version:          1,
		TotalInfractions: 1,
		Infractions:      []Infraction{{RuleID: "HISS-04", FilePath: "old.go", Fingerprint: "fp1"}},
	}

	// Negative: Touched legacy file revokes exemption
	current := []Infraction{{RuleID: "HISS-04", FilePath: "old.go", Fingerprint: "fp1"}}
	if res := EvaluateRatchet(b, current, []string{"old.go"}); res.Passed {
		t.Fatal("expected failure on touched legacy file")
	}

	// Negative: New violation in untouched file
	unbaselined := []Infraction{
		{RuleID: "HISS-04", FilePath: "old.go", Fingerprint: "fp1"},
		{RuleID: "HISS-07", FilePath: "other.go", Fingerprint: "fp2"},
	}
	resNew := EvaluateRatchet(b, unbaselined, []string{})
	if resNew.Passed || len(resNew.NewViolations) != 1 {
		t.Fatalf("expected 1 new violation in untouched file, got %+v", resNew)
	}

	// Boundary: Empty baseline and empty current
	emptyBase := &Baseline{}
	if res := EvaluateRatchet(emptyBase, []Infraction{}, []string{}); !res.Passed {
		t.Fatal("expected empty baseline to pass")
	}
}

func TestLoadBaseline_PositiveAndMissing(t *testing.T) {
	tmpDir := t.TempDir()
	missingPath := filepath.Join(tmpDir, "nonexistent.json")

	// Missing file returns empty baseline with Version 1
	base, err := LoadBaseline(missingPath)
	if err != nil || base.Version != 1 || base.TotalInfractions != 0 {
		t.Fatalf("unexpected missing baseline result: base=%+v, err=%v", base, err)
	}

	// Save and then load real baseline
	base.Repository = "cordanaLLM/praetor"
	base.Infractions = []Infraction{{RuleID: "HISS-01", FilePath: "main.go", Fingerprint: "fp_test"}}
	savePath := filepath.Join(tmpDir, "baseline.json")
	if err := SaveBaseline(savePath, base); err != nil {
		t.Fatalf("SaveBaseline failed: %v", err)
	}

	loaded, err := LoadBaseline(savePath)
	if err != nil || loaded.Repository != "cordanaLLM/praetor" || loaded.TotalInfractions != 1 {
		t.Fatalf("loaded baseline mismatch: loaded=%+v, err=%v", loaded, err)
	}
}

// TestLoadBaseline_AbsentMarksOnlyAMissingFile pins BUG-802: the empty snapshot for a
// missing file is distinguishable from a recorded zero, and the marker is never saved.
func TestLoadBaseline_AbsentMarksOnlyAMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing, err := LoadBaseline(filepath.Join(dir, "absent.json"))
	if err != nil || !missing.Absent {
		t.Fatalf("missing file: Absent=%v err=%v, want Absent", missing != nil && missing.Absent, err)
	}

	path := filepath.Join(dir, "zero.json")
	if err := SaveBaseline(path, missing); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "absent") {
		t.Fatalf("the Absent marker leaked into the saved file: %s", data)
	}
	recorded, err := LoadBaseline(path)
	if err != nil || recorded.Absent || recorded.TotalInfractions != 0 {
		t.Fatalf("recorded zero baseline: %+v, %v; want present with 0 infractions", recorded, err)
	}
}

func TestLoadBaseline_NegativeAndSaveErrors(t *testing.T) {
	tmpDir := t.TempDir()
	corruptPath := filepath.Join(tmpDir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{invalid-json"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadBaseline(corruptPath); err == nil {
		t.Fatal("expected error loading corrupt JSON")
	}

	// Invalid path for SaveBaseline
	base := &Baseline{Version: 1}
	if err := SaveBaseline("/dev/null/impossible/path.json", base); err == nil {
		t.Fatal("expected error saving to invalid path")
	}
}

// =========================================================================
// Cross-platform path separators (BUG-948)
// =========================================================================

// windowsInfraction is the record cordanaLLM/imago actually has committed, written by a scan on
// Windows. The Linux scan of the same file produces the forward-slash form below.
func windowsInfraction() Infraction {
	return Infraction{
		RuleID:      "HISS-02",
		FilePath:    `scripts\tunnel_hindsight.py`,
		LineNumber:  53,
		Message:     "Legacy unbounded while True loop in Python",
		Fingerprint: `scripts\tunnel_hindsight.py:53:HISS-02`,
	}
}

// linuxInfraction is the same infraction as a Linux scan reports it.
func linuxInfraction() Infraction {
	return Infraction{
		RuleID:      "HISS-02",
		FilePath:    "scripts/tunnel_hindsight.py",
		LineNumber:  53,
		Message:     "Legacy unbounded while True loop in Python",
		Fingerprint: "scripts/tunnel_hindsight.py:53:HISS-02",
	}
}

// TestEvaluateRatchet_Positive_BaselineWrittenOnAnotherPlatformStillSuppresses is the measured
// defect. A baseline recorded on Windows must suppress the same infraction scanned on Linux;
// before this, the separators differed, the fingerprints did not match, and the ratchet reported
// a decade-old baselined violation as brand new, blocking every commit.
func TestEvaluateRatchet_Positive_BaselineWrittenOnAnotherPlatformStillSuppresses(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{windowsInfraction()}}
	res := EvaluateRatchet(b, []Infraction{linuxInfraction()}, nil)
	if len(res.NewViolations) != 0 {
		t.Errorf("a Windows-written baseline must suppress the Linux scan of the same infraction, got %d new", len(res.NewViolations))
	}
	if !res.Passed {
		t.Error("the ratchet must pass when the only infraction is baselined on another platform")
	}
	// And the reverse direction, for a Windows checkout of a Linux-written baseline.
	rev := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{linuxInfraction()}}
	if got := EvaluateRatchet(rev, []Infraction{windowsInfraction()}, nil); len(got.NewViolations) != 0 {
		t.Errorf("the reverse direction must hold too, got %d new", len(got.NewViolations))
	}
}

// TestEvaluateRatchet_Negative_TouchedFileRuleFiresAcrossSeparators covers the worse half.
// Touched files come from Git, which always reports forward slashes, while a Windows scan
// reports backslashes. The two never compared equal, so on Windows the touched-file clean rule
// silently never fired and a violation in an edited file passed the gate.
func TestEvaluateRatchet_Negative_TouchedFileRuleFiresAcrossSeparators(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{windowsInfraction()}}
	res := EvaluateRatchet(b, []Infraction{windowsInfraction()}, []string{"scripts/tunnel_hindsight.py"})
	if len(res.TouchedCleanViolations) != 1 {
		t.Errorf("editing a file must revoke its baseline exemption whatever separator the scan used, got %d", len(res.TouchedCleanViolations))
	}
	if res.Passed {
		t.Error("a violation in a touched file must fail the ratchet")
	}
}

// TestNormalizePath_Boundary covers the normalisation itself, including the cases it must not
// change and the pathological one whose cost is accepted deliberately.
func TestNormalizePath_Boundary(t *testing.T) {
	cases := map[string]string{
		`scripts\tunnel_hindsight.py`: "scripts/tunnel_hindsight.py",
		"scripts/tunnel_hindsight.py": "scripts/tunnel_hindsight.py",
		`a\b\c.go:12:HISS-01`:         "a/b/c.go:12:HISS-01",
		"":                            "",
		"no-separators.go":            "no-separators.go",
		`mixed/style\path.go`:         "mixed/style/path.go",
		`weird\\double.go`:            "weird//double.go",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
