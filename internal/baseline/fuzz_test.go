package baseline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxFuzzTouched bounds the touched-file list built from one fuzz input (HISS-02).
const maxFuzzTouched = 16

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
	f.Add(validBytes, "")
	f.Add(validBytes, "b.go")

	f.Fuzz(func(t *testing.T, data []byte, touchedStr string) {
		baseFile := filepath.Join(t.TempDir(), "baseline.json")
		if err := os.WriteFile(baseFile, data, 0o600); err != nil {
			return
		}
		base, err := LoadBaseline(baseFile)
		if err != nil {
			return
		}

		touched := fuzzTouched(touchedStr)
		current := fuzzCurrent(base, touchedStr)
		res := EvaluateRatchet(base, current, touched)
		if res == nil {
			t.Fatal("EvaluateRatchet returned nil")
		}
		assertRatchetCountsAgree(t, base, current, res)
		assertRatchetPartitionsViolations(t, base, touched, res)
	})
}

// fuzzTouched turns the fuzzed string into a bounded touched-file list.
func fuzzTouched(touchedStr string) []string {
	if touchedStr == "" || len(touchedStr) >= 256 {
		return nil
	}
	parts := strings.Split(touchedStr, ",")
	if len(parts) > maxFuzzTouched {
		parts = parts[:maxFuzzTouched]
	}
	return parts
}

// fuzzCurrent builds the scan result the ratchet judges. It is the baseline's own
// infractions plus one infraction the baseline never recorded, so the new-violation and
// touched-file branches are reachable; passing the baseline back unchanged, as this target
// used to, made both branches dead whatever the fuzzer supplied.
func fuzzCurrent(base *Baseline, touchedStr string) []Infraction {
	current := make([]Infraction, 0, len(base.Infractions)+1)
	current = append(current, base.Infractions...)
	file := "fuzz-new.go"
	if parts := fuzzTouched(touchedStr); len(parts) > 0 {
		file = parts[0]
	}
	return append(current, Infraction{
		RuleID:      "HISS-04",
		FilePath:    file,
		LineNumber:  1,
		Fingerprint: "fuzz-unbaselined-fingerprint",
	})
}

// assertRatchetCountsAgree pins the counts against the inputs that produced them.
func assertRatchetCountsAgree(t *testing.T, base *Baseline, current []Infraction, res *RatchetResult) {
	t.Helper()
	if res.PreviousCount != base.TotalInfractions {
		t.Fatalf("PreviousCount %d does not match the baseline total %d", res.PreviousCount, base.TotalInfractions)
	}
	if res.CurrentCount != len(current) {
		t.Fatalf("CurrentCount %d does not match the %d violations judged", res.CurrentCount, len(current))
	}
	if len(res.NewViolations)+len(res.TouchedCleanViolations) > res.CurrentCount {
		t.Fatalf("reported %d new and %d touched-clean violations out of %d judged",
			len(res.NewViolations), len(res.TouchedCleanViolations), res.CurrentCount)
	}
	if res.Passed && (len(res.NewViolations) > 0 || len(res.TouchedCleanViolations) > 0) {
		t.Fatalf("a passing ratchet cannot report violations: %+v", res)
	}
	if res.Passed && res.CurrentCount > res.PreviousCount {
		t.Fatalf("a passing ratchet cannot raise the count: %d > %d", res.CurrentCount, res.PreviousCount)
	}
}

// assertRatchetPartitionsViolations pins what each reported violation means: a new
// violation is one the baseline does not carry, in a file nobody touched, and a
// touched-clean violation is one in a touched file. Nothing may appear in both.
func assertRatchetPartitionsViolations(t *testing.T, base *Baseline, touched []string, res *RatchetResult) {
	t.Helper()
	baselined := make(map[string]struct{}, len(base.Infractions))
	for _, inf := range base.Infractions {
		baselined[NormalizePath(inf.Fingerprint)] = struct{}{}
	}
	touchedFiles := make(map[string]struct{}, len(touched))
	for _, f := range touched {
		touchedFiles[NormalizePath(f)] = struct{}{}
	}
	inTouchedClean := make(map[string]struct{}, len(res.TouchedCleanViolations))
	for _, v := range res.TouchedCleanViolations {
		if _, ok := touchedFiles[NormalizePath(v.FilePath)]; !ok {
			t.Fatalf("touched-clean violation in an untouched file: %+v (touched=%v)", v, touched)
		}
		inTouchedClean[v.Fingerprint] = struct{}{}
	}
	for _, v := range res.NewViolations {
		if _, ok := baselined[NormalizePath(v.Fingerprint)]; ok {
			t.Fatalf("baselined infraction reported as new: %+v", v)
		}
		if _, ok := touchedFiles[NormalizePath(v.FilePath)]; ok {
			t.Fatalf("violation in a touched file reported as new rather than touched-clean: %+v", v)
		}
		if _, ok := inTouchedClean[v.Fingerprint]; ok {
			t.Fatalf("violation reported as both new and touched-clean: %+v", v)
		}
	}
}
