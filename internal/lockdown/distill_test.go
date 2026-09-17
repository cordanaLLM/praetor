package lockdown

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

var evidencePointerLine = regexp.MustCompile(`^evidence: .+ sha256:[0-9a-f]{12} lines:\d+$`)

func verboseSARIF(t *testing.T, results int) []byte {
	t.Helper()
	items := make([]SarifResult, 0, results)
	for i := 0; i < results; i++ {
		items = append(items, SarifResult{
			RuleID:  fmt.Sprintf("HISS-%02d", i+1),
			Level:   LevelError,
			Message: SarifMessage{Text: strings.Repeat("an overlong diagnostic message ", 400)},
		})
	}
	payload, err := json.Marshal(SarifLog{Version: "2.1.0", Runs: []SarifRun{{Results: items}}})
	if err != nil {
		t.Fatalf("marshal SARIF: %v", err)
	}
	return payload
}

// The distillation cap is the default inline evidence bound by construction, so the two
// numbers cannot drift apart.
func TestDistillBoundsAliasTheEvidenceDefaults(t *testing.T) {
	if MaxDistillLines != config.EvidenceInlineMaxLinesDefault || MaxDistillTokens != config.EvidenceInlineMaxTokensDefault {
		t.Fatalf("distill bounds %d/%d differ from the evidence defaults %d/%d",
			MaxDistillLines, MaxDistillTokens, config.EvidenceInlineMaxLinesDefault, config.EvidenceInlineMaxTokensDefault)
	}
}

func TestDistillSummaryEndsWithEvidencePointer(t *testing.T) {
	dir := t.TempDir()
	for name, payload := range map[string][]byte{
		"truncated": verboseSARIF(t, 3),
		"complete":  []byte("{\"version\":\"2.1.0\",\"runs\":[]}\n"),
	} {
		res, err := DistillSARIF(context.Background(), payload, dir, dir)
		if err != nil {
			t.Fatalf("%s: DistillSARIF: %v", name, err)
		}
		lines := strings.Split(res.Summary, "\n")
		last := lines[len(lines)-1]
		if !evidencePointerLine.MatchString(last) || !strings.Contains(last, res.FullReportPath) {
			t.Errorf("%s: last summary line %q is not the pointer to %s", name, last, res.FullReportPath)
		}
		if truncated := strings.Contains(res.Summary, "[Truncated:"); truncated != (name == "truncated") {
			t.Errorf("%s: truncation marker present = %v", name, truncated)
		}
	}
}

func TestSarifEvidencePointerCountsLines(t *testing.T) {
	for body, want := range map[string]string{"": "lines:0", "{}": "lines:1", "{}\n": "lines:1", "{\n}\n": "lines:2"} {
		pointer, err := sarifEvidencePointer("full.sarif", []byte(body))
		if err != nil || !strings.HasSuffix(pointer, want) {
			t.Errorf("body %q: pointer = %q, %v; want suffix %s", body, pointer, err, want)
		}
	}
	if _, err := sarifEvidencePointer("two\nlines.sarif", []byte("{}")); err == nil {
		t.Error("a path that breaks the pointer line must be an error")
	}
}

func TestCapSummaryLineBoundary(t *testing.T) {
	pointer := "evidence: full.sarif sha256:0123456789ab lines:1"
	raw := make([]string, 0, MaxDistillLines)
	for i := 0; i < MaxDistillLines-2; i++ {
		raw = append(raw, fmt.Sprintf("line %d", i))
	}
	summary, count, _ := capSummary(raw, pointer)
	if count != MaxDistillLines-2 || strings.Contains(summary, "[Truncated:") {
		t.Fatalf("%d raw lines must pass untruncated, got %d lines", MaxDistillLines-2, count)
	}
	summary, count, _ = capSummary(append(raw, "one more"), pointer)
	if count != MaxDistillLines || !strings.HasSuffix(summary, "\n"+pointer) {
		t.Fatalf("one more raw line must truncate to %d lines ending in the pointer, got %d:\n%s", MaxDistillLines, count, summary)
	}
}

func TestDistillRejectsUnwritableEphemeralDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	_, err := DistillSARIF(context.Background(), []byte(`{"version":"2.1.0","runs":[]}`), t.TempDir(), filepath.Join(blocker, "sarif"))
	if err == nil {
		t.Fatal("an ephemeral directory below a regular file must be an error")
	}
}
