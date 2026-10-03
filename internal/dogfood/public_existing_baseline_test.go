package dogfood

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// publicDebtSource is Go source carrying one HISS infraction: a discarded error result.
const publicDebtSource = "package fixture\n\nfunc run() {\n\t_ = doSomething()\n}\n"

// publicRecordedBaseline renders a baseline file recording one infraction in file.
func publicRecordedBaseline(file string) string {
	return `{"version":1,"generated_at":"2026-01-01T00:00:00Z","repository":"","commit_sha":"","total_infractions":1,` +
		`"infractions":[{"rule_id":"HISS-07","file_path":"` + file + `","line_number":4,` +
		`"message":"Legacy unchecked error assignment","fingerprint":"` + file + `:4:HISS-07"}]}` + "\n"
}

// TestRunPublicLoopReRecordsAnExistingBaseline: the loop anchors the adopted baseline to the
// independent scan of the original tree, so on a disposable checkout that already carries a
// baseline it re-records explicitly, where a plain adoption keeps the file (#358). Positive: a
// baseline recording debt the tree no longer has is lowered to the scan. Boundary: a baseline
// that lags the tree is raised to the scan, with the loop's rationale. Negative: an unreadable
// baseline fails the plan instead of being replaced.
func TestRunPublicLoopReRecordsAnExistingBaseline(t *testing.T) {
	for name, test := range map[string]struct {
		files     map[string]string
		count     int
		rationale bool
	}{
		"stale entry lowered": {files: map[string]string{".standards-baseline.json": publicRecordedBaseline("gone.go")}},
		"lagging baseline raised": {files: map[string]string{
			".standards-baseline.json": publicRecordedBaseline("debt.go"), "debt.go": publicDebtSource,
			"more.go": strings.Replace(publicDebtSource, "run", "more", 1),
		}, count: 2, rationale: true},
	} {
		t.Run(name, func(t *testing.T) {
			opts, _ := publicLoopFixture(t, test.files)
			opts.Apply = true
			report, err := RunPublicLoop(context.Background(), opts)
			if err != nil || !report.Verified {
				t.Fatalf("public loop over an existing baseline = %v, %+v; want a verified run", err, report)
			}
			result := report.Results[0]
			recorded, err := baseline.LoadBaseline(filepath.Join(result.Checkout, ".standards-baseline.json"))
			if err != nil || recorded.TotalInfractions != test.count || (recorded.IncreaseRationale != "") != test.rationale {
				t.Fatalf("adopted baseline = %+v, %v; want %d infractions, rationale %v", recorded, err, test.count, test.rationale)
			}
			if result.Plan.LegacyDebtCount != test.count || result.Attempts[0].Adoption.LegacyDebtCount != test.count {
				t.Fatalf("plan %d, first attempt %d; want the original scan's %d infractions",
					result.Plan.LegacyDebtCount, result.Attempts[0].Adoption.LegacyDebtCount, test.count)
			}
		})
	}

	opts, _ := publicLoopFixture(t, map[string]string{".standards-baseline.json": "{"})
	opts.Apply = true
	report, err := RunPublicLoop(context.Background(), opts)
	if err == nil || report.Verified || !strings.Contains(report.Results[0].Error, "plan: load existing baseline") {
		t.Fatalf("public loop over an unreadable baseline = %v, %+v; want a failed plan naming it", err, report)
	}
}
