package baseline

import (
	"os"
	"path/filepath"
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
