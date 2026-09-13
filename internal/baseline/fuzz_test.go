package baseline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func FuzzBaselineRatchet(f *testing.F) {
	validBase := Baseline{
		Version:          1,
		TotalInfractions: 2,
		Infractions: []Infraction{
			{RuleID: "HISS-01", FilePath: "a.go", Fingerprint: "fp1"},
			{RuleID: "HISS-02", FilePath: "b.go", Fingerprint: "fp2"},
		},
	}
	validBytes, err := json.Marshal(validBase)
	if err != nil {
		f.Fatalf("marshal seed baseline: %v", err)
	}
	f.Add(validBytes, "a.go,c.go")

	f.Fuzz(func(t *testing.T, data []byte, touchedStr string) {
		tmpDir := t.TempDir()
		baseFile := filepath.Join(tmpDir, "baseline.json")
		if err := os.WriteFile(baseFile, data, 0644); err != nil {
			return
		}

		base, err := LoadBaseline(baseFile)
		if err != nil {
			return
		}

		touched := []string{}
		if len(touchedStr) > 0 && len(touchedStr) < 256 {
			touched = append(touched, touchedStr)
		}

		res := EvaluateRatchet(base, base.Infractions, touched)
		if res == nil {
			t.Fatal("EvaluateRatchet returned nil")
		}
	})
}
