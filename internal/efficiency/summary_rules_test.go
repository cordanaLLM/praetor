// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// labelled is one records-row vector field with a provenance label.
func labelled(value, provenance string) string {
	return `{"value":` + value + `,"provenance":"` + provenance + `"}`
}

// recordRow is one records row; wall overrides the wall_seconds field.
func recordRow(number, branch, disposition, wall string) string {
	return `{"number":` + number + `,"head_branch":"` + branch + `","title":"Unit ` + number + `","disposition":"` + disposition + `","metric_epoch":"2026-10-10",` +
		`"created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-02T12:00:00Z",` +
		`"closing_issues":[{"number":1` + number + `,"created_at":"2026-10-02T09:00:00Z"}],` +
		`"tokens_by_provider":` + labelled(`{"anthropic":100}`, "measured") + `,"wall_seconds":` + wall + `,` +
		`"review_rounds":` + labelled("2", "measured") + `,"retries":` + labelled("1", "measured") + `,` +
		`"operator_minutes":` + labelled("4", "measured") + `,"escaped_defects":` + labelled("0", "measured") + `}`
}

func collectRows(t *testing.T, transcripts bool, rows ...string) MilestoneSummary {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prs.json")
	writeFile(t, path, []byte("["+strings.Join(rows, ",")+"]"))
	opts := CollectorOptions{ForgeJSONPath: path}
	if transcripts {
		opts.TranscriptsDir = filepath.Join(dir, "tr")
		writeFile(t, filepath.Join(opts.TranscriptsDir, "s.jsonl"), readFixture(t))
	}
	return collect(t, opts).MilestoneSummary
}

// is reports a set pointer holding want.
func is[T comparable](p *T, want T) bool {
	return p != nil && *p == want
}

// expectation is one named check of a test table: ok must hold, got is printed when it does not.
type expectation struct {
	name string
	ok   bool
	got  any
}

func expectAll(t *testing.T, checks []expectation) {
	t.Helper()
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: got %+v", c.name, c.got)
		}
	}
}

// The resource rule keeps a failed unit's consumption in the numerator and divides by the
// qualified units; the ratio rule pools every unit's usage. The reverted unit is the only one
// with sessions (feat/x), so a qualified-only filter in either rule changes every figure here.
func TestSummary_Positive_FailedUnitsStayInResourceNumeratorsAndRatios(t *testing.T) {
	ms := collectRows(t, true,
		recordRow("1", "feat/x", "reverted", labelled("100", "measured")),
		recordRow("2", "feat/y", "qualified", labelled("100", "measured")))
	touches := ">= 2.00 per qualified unit (lower bound: total 2 over 1 of 2 units measured / 1 qualified)"
	pooled := "pooled over the usage of 1 of 2 units"
	expectAll(t, []expectation{
		{"units and qualified units", ms.UnitsCount == 2, ms.UnitsCount},
		{"one qualified unit", ms.QualifiedUnits == 1, ms.QualifiedUnits},
		{"operator touches total keeps the reverted unit", is(ms.OperatorTouchNum, 2), ms.OperatorTouchNum},
		{"operator touches per qualified unit", is(ms.OperatorTouchesPerQualified, 2.0), ms.OperatorTouchesPerQualified},
		{"operator touches text", ms.OperatorTouches == touches, ms.OperatorTouches},
		{"frontier tokens total keeps the reverted unit", is(ms.FrontierTokensNum, int64(1150)), ms.FrontierTokensNum},
		{"frontier tokens per qualified unit", is(ms.FrontierTokensPerQualifiedUnit, 1150.0), ms.FrontierTokensPerQualifiedUnit},
		{"local-first ratio over all usage", is(ms.LocalRatio, 0.0), ms.LocalRatio},
		{"local-first text names the pool", strings.Contains(ms.LocalFirstRatio, pooled), ms.LocalFirstRatio},
		{"prompt-cache hit rate over all usage", ms.CacheHitRatio != nil, ms.CacheHitRatio},
		{"prompt-cache text names the pool", strings.Contains(ms.PromptCacheHitRate, pooled), ms.PromptCacheHitRate},
		{"review rounds per qualified unit (2+2 over 1)", perQualified(t, "review rounds", ms.ReviewRounds, "") == 4, ms.ReviewRounds},
		{"issue-to-merge counts qualified units only", ms.IssueToMergeUnits == 1, ms.IssueToMergeUnits},
		{"issue-to-merge is a qualified mean", strings.HasPrefix(ms.AvgIssueToMerge, "3h00m (mean over 1 of 1 qualified units measured)"), ms.AvgIssueToMerge},
	})
}

// The ratio rule pools counts; a mean of per-unit ratios would weigh a one-request unit like a
// hundred-request one.
func TestSummary_Positive_RatiosPoolCountsNotUnitMeans(t *testing.T) {
	ints := func(v int) *int { return &v }
	int64s := func(v int64) *int64 { return &v }
	report := newReport("")
	report.Units = []UnitReport{
		{Disposition: DispositionReverted, RequestsNum: ints(1), LocalRequestsNum: ints(1), CacheReadTokensNum: int64s(90), PromptInputTokensNum: int64s(100)},
		{Disposition: DispositionQualified, RequestsNum: ints(3), LocalRequestsNum: ints(0), CacheReadTokensNum: int64s(0), PromptInputTokensNum: int64s(900)},
	}
	if err := (&Collector{}).buildMilestoneSummary(report, nil); err != nil {
		t.Fatal(err)
	}
	ms := report.MilestoneSummary
	if ms.LocalRatio == nil || *ms.LocalRatio != 0.25 {
		t.Errorf("local-first pooled 1/4, not the unit mean 0.5: %v", ms.LocalRatio)
	}
	if ms.CacheHitRatio == nil || *ms.CacheHitRatio != 0.09 {
		t.Errorf("cache hit pooled 90/1000, not the unit mean 0.45: %v", ms.CacheHitRatio)
	}
}

// A summary that adds values of different provenance names the mix, and an interval field's
// bounds carry through to the total and the per-qualified rate.
func TestSummary_Positive_ProvenanceMixAndIntervalBounds(t *testing.T) {
	ms := collectRows(t, false,
		recordRow("1", "feat/a", "qualified", labelled("100", "measured")),
		recordRow("2", "feat/b", "qualified", `{"value":200,"provenance":"interval","low":150,"high":300}`),
		recordRow("3", "feat/c", "rejected", labelled("60", "modeled")))
	w := ms.WallSeconds
	c := w.Components[0]
	display := "3m00s [2m35s, 3m50s] per qualified unit (total 6m00s [5m10s, 7m40s] over 3 units / 2 qualified) [measured 1, modeled 1, interval 1]"
	expectAll(t, []expectation{
		{"provenance mix", w.Provenance == ProvenanceMix{Measured: 1, Modeled: 1, Interval: 1}, w.Provenance},
		{"total over every unit", c.Total == 360, c},
		{"total bounds", is(c.TotalBounds, Interval{Low: 310, High: 460}), c.TotalBounds},
		{"per qualified unit", is(c.PerQualifiedUnit, 180.0), c.PerQualifiedUnit},
		{"per-qualified bounds", is(c.PerQualifiedUnitBounds, Interval{Low: 155, High: 230}), c.PerQualifiedUnitBounds},
		{"display", w.Display == display, w.Display},
		{"no bounds without an interval", ms.ReviewRounds.Components[0].TotalBounds == nil, ms.ReviewRounds},
		{"mix of a single label", strings.HasSuffix(ms.ReviewRounds.Display, "[measured 3]"), ms.ReviewRounds.Display},
	})
}

// A field some units do not measure only bounds the cost from below, and says so.
func TestSummary_Boundary_PartialCoverageIsALowerBound(t *testing.T) {
	ms := collectRows(t, false,
		recordRow("1", "feat/a", "qualified", labelled("100", "measured")),
		recordRow("2", "feat/b", "qualified", "null"))
	w := ms.WallSeconds
	if !w.LowerBound || w.UnitsMeasured != 1 {
		t.Errorf("coverage: %+v", w)
	}
	if want := ">= 50s per qualified unit (lower bound: total 1m40s over 1 of 2 units measured / 2 qualified) [measured 1]"; w.Display != want {
		t.Errorf("display = %q, want %q", w.Display, want)
	}
}

func TestCollector_Negative_RecordsRowOmittingAVectorFieldRefused(t *testing.T) {
	row := strings.Replace(recordRow("1", "feat/a", "qualified", labelled("1", "measured")), `"retries":`+labelled("1", "measured")+`,`, "", 1)
	path := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, path, []byte("["+row+"]"))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: path}).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), `field "retries": missing`) {
		t.Errorf("a records row without a vector field must be refused naming it: %v", err)
	}
}
