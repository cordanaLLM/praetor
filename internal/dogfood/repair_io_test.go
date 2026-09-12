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
	if _, err := LoadRepairReport(nil, target); err == nil {
		t.Fatal("nil read context accepted")
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
