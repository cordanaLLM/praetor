package dogfood

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairReportFileAndOutputBounds(t *testing.T) {
	body := repairReportJSON(t)
	exact := repairTestFile(t, body+strings.Repeat(" ", maxRepairReportBytes-len(body)))
	if _, err := LoadRepairReport(context.Background(), exact); err != nil {
		t.Fatalf("exact byte bound: %v", err)
	}
	if err := os.Truncate(exact, maxRepairReportBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepairReport(context.Background(), exact); err == nil {
		t.Fatal("oversized report accepted")
	}
	target := repairTestFile(t, body)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepairReport(context.Background(), link); err == nil {
		t.Fatal("symlink report accepted")
	}
	dirLink := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(filepath.Dir(target), dirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepairReport(context.Background(), filepath.Join(dirLink, "input")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	plan, err := PlanRepairs(context.Background(), repairTestReport(t, 1), repairTestPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRepairPlan(context.Background(), filepath.Join(dirLink, "review"), plan); err == nil {
		t.Fatal("symlink output ancestor accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, invalid := range []context.Context{nil, cancelled} {
		if _, err := LoadRepairReport(invalid, target); err == nil {
			t.Fatal("invalid read context accepted")
		}
	}
}

func TestRepairJSONDepthTokensAndEscapes(t *testing.T) {
	if err := validateRepairJSON([]byte(strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33))); err == nil {
		t.Fatal("depth overflow accepted")
	}
	if err := validateRepairJSON([]byte("[" + strings.Repeat("0,", maxRepairJSONTokens) + "0]")); err == nil {
		t.Fatal("token overflow accepted")
	}
	for _, text := range []string{`"\udfff"`, `"\ud800\u0041"`, `"\ud800x"`} {
		if err := validateRepairJSON([]byte(text)); err == nil {
			t.Fatal("bad surrogate accepted")
		}
	}
	if err := validateRepairJSON([]byte{'"', 0xff, '"'}); err == nil {
		t.Fatal("invalid UTF8 accepted")
	}
	if err := validateRepairJSON([]byte("[" + strings.Repeat("0,", maxRepairJSONTokens-3) + "0]")); err != nil {
		t.Fatalf("document of exactly %d tokens refused: %v", maxRepairJSONTokens, err)
	}
	if err := validateRepairJSON([]byte(strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32))); err != nil {
		t.Fatalf("32 nesting levels refused: %v", err)
	}
}

// TestRepairJSONRefusesCaseFoldedNames covers issue #310. The engine_build map decodes both
// spellings of a name, so the shape check passed a report holding "revision" and "Revision";
// the strict reader now refuses case-folded names at every depth, as it refuses exact ones.
func TestRepairJSONRefusesCaseFoldedNames(t *testing.T) {
	report := repairTestReport(t, 1)
	report.Engine = map[string]string{"revision": "recorded"}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	if _, err := LoadRepairReport(context.Background(), repairTestFile(t, valid)); err != nil {
		t.Fatalf("report with an engine_build map refused: %v", err)
	}
	for _, body := range []string{
		strings.Replace(valid, `"engine_build":{`, `"engine_build":{"Revision":"shadow",`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"VERSION":1`, 1),
	} {
		if body == valid {
			t.Fatal("report fixture lost the member this test shadows")
		}
		if _, err := LoadRepairReport(context.Background(), repairTestFile(t, body)); err == nil || err.Error() != "duplicate repair JSON field" {
			t.Errorf("case-folded duplicate returned %v", err)
		}
	}
}

func TestRepairRejectsVerifiedPublicStub(t *testing.T) {
	report := repairTestReport(t, 1)
	report.Status, report.Verified = "verified", true
	report.Cases[0] = SuiteCase{ID: "public-01", Kind: "public", Status: "verified", Repository: "https://github.com/spf13/cobra#" + strings.Repeat("a", 40), Public: &PublicLoopReport{Verified: true}}
	if _, err := PlanRepairs(context.Background(), report, repairTestPolicy(t)); err == nil {
		t.Fatal("public verified boolean stub accepted")
	}
}

func TestRepairStrictShapeDoesNotLeakContent(t *testing.T) {
	body := strings.Replace(repairReportJSON(t), `"version":1`, `"version":"secret-string"`, 1)
	_, err := LoadRepairReport(context.Background(), repairTestFile(t, body))
	if err == nil || strings.Contains(err.Error(), "secret-string") {
		t.Fatalf("private input echoed: %v", err)
	}
	report := repairTestReport(t, 1)
	report.Cases[0].Error = "literal " + string(rune(0xfffd))
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepairReport(context.Background(), repairTestFile(t, string(data))); err != nil {
		t.Fatal(err)
	}
}
