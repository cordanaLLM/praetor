// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/router"
)

func ptrFloat64(f float64) *float64 {
	return &f
}

type stubForge struct {
	forge.Forge
	list  forge.MergedPullRequestList
	err   error
	query forge.MergedPullRequestQuery
}

func (s *stubForge) ListMergedPullRequests(_ context.Context, q forge.MergedPullRequestQuery) (forge.MergedPullRequestList, error) {
	s.query = q
	return s.list, s.err
}

const forgeRecords = `[
 {"number":1,"head_branch":"feat/x","title":"One","milestone":"M1","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-01T10:00:00Z","merged_at":"2026-10-04T10:00:00Z",
  "closing_issues":[{"number":10,"created_at":"2026-10-01T00:00:00Z"}],
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}},
 {"number":2,"head_branch":"feat/y","title":"Two","milestone":"M1","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-03T10:00:00Z",
  "closing_issues":[{"number":11}],
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}},
 {"number":3,"head_branch":"feat/z","title":"Three","milestone":"M1","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-02T10:00:00Z",
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}},
 {"number":4,"head_branch":"feat/o","title":"Other","milestone":"M2","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-09T10:00:00Z",
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}}
]`

func collect(t *testing.T, opts CollectorOptions) *Report {
	t.Helper()
	report, err := NewCollector(opts).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func unitByNumber(t *testing.T, r *Report, n int) UnitReport {
	t.Helper()
	for _, u := range r.Units {
		if u.PullRequestNumber == n {
			return u
		}
	}
	t.Fatalf("unit #%d missing in %+v", n, r.Units)
	return UnitReport{}
}

func TestCollector_Positive_IssueToMergeNeverFallsBackToPRAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prs.json")
	writeFile(t, path, []byte(forgeRecords))
	report := collect(t, CollectorOptions{ForgeJSONPath: path, Milestone: "M1"})
	if len(report.Units) != 3 {
		t.Fatalf("units: %d", len(report.Units))
	}
	if got := unitByNumber(t, report, 1).IssueToMerge; got != "3d10h" {
		t.Errorf("#1 issue-to-merge = %q", got)
	}
	for _, n := range []int{2, 3} {
		if got := unitByNumber(t, report, n).IssueToMerge; got != NotMeasured {
			t.Errorf("#%d without a closing issue time must print %q, got %q", n, NotMeasured, got)
		}
	}
	if !strings.Contains(report.MilestoneSummary.AvgIssueToMerge, "3d10h (mean over 1 of 3 qualified units measured)") {
		t.Errorf("average must exclude unmeasured units and say so: %q", report.MilestoneSummary.AvgIssueToMerge)
	}
}

func TestCollector_Positive_MilestoneFiltersBeforeLimitAndStatesTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, path, []byte(forgeRecords))
	report := collect(t, CollectorOptions{ForgeJSONPath: path, Milestone: "M1", Limit: 2})
	if len(report.Units) != 2 || report.Units[0].PullRequestNumber != 1 || report.Units[1].PullRequestNumber != 2 {
		t.Fatalf("want the two newest M1 merges (#4 of M2 is newer but filtered first): %+v", report.Units)
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "1 matching pull requests beyond the limit of 2") {
		t.Fatalf("truncation must be stated: %v", report.Notes)
	}
}

func TestCollector_Positive_LiveDriverQueryAndNotes(t *testing.T) {
	stub := &stubForge{list: forge.MergedPullRequestList{
		// The shape ghPullToMerged returns: no epoch, disposition or vector field.
		PullRequests: []forge.MergedPullRequest{{
			Number:     7,
			HeadBranch: "b",
			Title:      "Stub PR",
			CreatedAt:  time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
			MergedAt:   time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC),
		}},
		Truncated: "scanned only 2000",
		Warnings:  []string{"closing issue #9 not fetched"},
	}}
	report := collect(t, CollectorOptions{ForgeDriver: stub, Milestone: "M1", Limit: 5, Notes: []string{"caller note"}})
	if stub.query.Limit != 5 || stub.query.Milestone != "M1" {
		t.Errorf("milestone and limit go to the forge together: %+v", stub.query)
	}
	joined := strings.Join(report.Notes, "\n")
	for _, want := range []string{"caller note", "live forge queries merged pull requests only", "forge listing incomplete: scanned only 2000", "forge: closing issue #9 not fetched", LiveDispositionNote, LiveUnmeasuredFields} {
		if !strings.Contains(joined, want) {
			t.Errorf("note %q missing in %q", want, joined)
		}
	}
	if !report.Sources.Forge || len(report.Units) != 1 {
		t.Fatalf("%+v", report)
	}
	assertLiveStamped(t, report.Units[0], 1800)
}

// assertLiveStamped checks a live row: current epoch, qualified, wall_seconds measured from the
// forge timestamps, every other vector field not measured.
func assertLiveStamped(t *testing.T, u UnitReport, wallSeconds int64) {
	t.Helper()
	expectAll(t, []expectation{
		{"current metric epoch", u.MetricEpoch == CurrentMetricEpoch, u.MetricEpoch},
		{"merged means qualified", u.Disposition == DispositionQualified, u.Disposition},
		{"wall_seconds measured from creation to merge", is(u.WallSeconds, VectorField[int64]{Value: wallSeconds, Provenance: ProvenanceMeasured}), u.WallSeconds},
		{"tokens_by_provider not measured", u.TokensByProvider == nil, u.TokensByProvider},
		{"review_rounds not measured", u.ReviewRounds == nil, u.ReviewRounds},
		{"retries not measured", u.Retries == nil, u.Retries},
		{"operator_minutes not measured", u.OperatorMinutes == nil, u.OperatorMinutes},
		{"escaped_defects not measured", u.EscapedDefects == nil, u.EscapedDefects},
	})
}

func TestCollector_Negative_SourceErrorsFailTheRun(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	boom := errors.New("boom")
	cases := map[string]CollectorOptions{
		"forge file missing":  {ForgeJSONPath: filepath.Join(dir, "none.json")},
		"forge driver error":  {ForgeDriver: &stubForge{err: boom}},
		"transcripts missing": {ForgeJSONPath: prs, TranscriptsDir: filepath.Join(dir, "none")},
		"spend log missing":   {ForgeJSONPath: prs, SpendLogPath: filepath.Join(dir, "none.jsonl")},
	}
	for name, opts := range cases {
		if _, err := NewCollector(opts).Collect(context.Background()); err == nil {
			t.Errorf("%s must fail the run", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewCollector(CollectorOptions{ForgeJSONPath: prs}).Collect(ctx); err == nil {
		t.Error("cancelled context must fail")
	}
}

func TestCollector_Negative_ZeroDenominatorsPrintNotMeasured(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, Milestone: "M1"})
	u := unitByNumber(t, report, 2)
	for name, got := range map[string]string{"cache": u.PromptCacheHitRate, "local": u.LocalFirstRatio, "touches": u.OperatorTouches, "tokens": u.FrontierTokens, "spend": u.Spend} {
		if got != NotMeasured {
			t.Errorf("unit without joined sessions: %s = %q, want %q", name, got, NotMeasured)
		}
	}
	empty := collect(t, CollectorOptions{ForgeJSONPath: prs, Milestone: "none"})
	ms := empty.MilestoneSummary
	if ms.UnitsCount != 0 || ms.LocalFirstRatio != UndefinedRate || ms.PromptCacheHitRate != UndefinedRate || ms.AvgIssueToMerge != UndefinedRate {
		t.Errorf("empty summary: %+v", ms)
	}
	none := collect(t, CollectorOptions{})
	if none.Sources.Forge || none.MilestoneSummary.TotalSpend != NotMeasured {
		t.Errorf("no sources: %+v", none)
	}
}

func assertUnaddedNote(t *testing.T, notes []string, want string) {
	t.Helper()
	for _, n := range notes {
		if strings.Contains(n, want) {
			return
		}
	}
	t.Errorf("expected note %q in %v", want, notes)
}

func assertNoUnaddedNote(t *testing.T, notes []string) {
	t.Helper()
	for _, n := range notes {
		if strings.Contains(n, "transcript requests not added") {
			t.Errorf("unexpected unadded transcripts note when false: %q", n)
		}
	}
}

func assertFalseGatewayUnit(t *testing.T, u UnitReport) {
	t.Helper()
	if u.FrontierTokens != "1150" {
		t.Errorf("expected 1150 frontier tokens, got %s", u.FrontierTokens)
	}
	if u.LocalFirstRatio != "33.3%" {
		t.Errorf("expected 33.3%% local-first, got %s", u.LocalFirstRatio)
	}
	if u.Sources != "transcripts+gateway" {
		t.Errorf("expected sources transcripts+gateway, got %q", u.Sources)
	}
	if u.OperatorTouches != "2" || u.PromptCacheHitRate == NotMeasured {
		t.Errorf("touches and cache hit still come from transcripts: %+v", u)
	}
	if u.TranscriptRequestsNotAdded != nil {
		t.Errorf("expected nil TranscriptRequestsNotAdded when false, got %v", *u.TranscriptRequestsNotAdded)
	}
}

func assertTrueGatewayUnit(t *testing.T, u UnitReport) {
	t.Helper()
	if u.FrontierTokens != "0" {
		t.Errorf("expected 0 frontier tokens, got %s", u.FrontierTokens)
	}
	if u.LocalFirstRatio != "100.0%" {
		t.Errorf("expected 100.0%% local-first, got %s", u.LocalFirstRatio)
	}
	if u.Sources != "gateway" {
		t.Errorf("expected sources gateway, got %q", u.Sources)
	}
	if u.OperatorTouches != "2" || u.PromptCacheHitRate == NotMeasured {
		t.Errorf("touches and cache hit still come from transcripts: %+v", u)
	}
	if u.TranscriptRequestsNotAdded == nil || *u.TranscriptRequestsNotAdded != 2 {
		t.Errorf("expected TranscriptRequestsNotAdded to be 2, got %v", u.TranscriptRequestsNotAdded)
	}
}

func TestCollector_Positive_TranscriptsViaGatewayFalseCombinesBoth(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	spend := filepath.Join(dir, "spend.jsonl")
	writeFile(t, spend, []byte(`{"request_id":"b","model":"ollama/q","model_group":"local","spend":0,"total_tokens":50,"request_tags":["branch:feat/x"]}
`))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, SpendLogPath: spend, Milestone: "M1"})
	u := unitByNumber(t, report, 1)
	assertFalseGatewayUnit(t, u)
	assertNoUnaddedNote(t, report.Notes)
}

func TestCollector_Positive_TranscriptsViaGatewayTrueGatewayCounts(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	spend := filepath.Join(dir, "spend.jsonl")
	writeFile(t, spend, []byte(`{"request_id":"b","model":"ollama/q","model_group":"local","spend":0,"total_tokens":50,"request_tags":["branch:feat/x"]}
`))
	policy := &config.EfficiencyPolicy{
		Sources: config.EfficiencySources{
			TranscriptsViaGateway: true,
		},
	}
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, SpendLogPath: spend, Milestone: "M1", Policy: policy})
	u := unitByNumber(t, report, 1)
	assertTrueGatewayUnit(t, u)
	assertUnaddedNote(t, report.Notes, "unit #1: 2 transcript requests not added (transcripts_via_gateway=true)")
}

// The milestone summary splits gateway spend into this milestone's units, other units and
// unattributed entries, and the parts add up to the total; the split does not depend on
// transcripts_via_gateway, which decides tokens only.
func TestCollector_Positive_MilestoneSpendSplitAddsUp(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	spend := filepath.Join(dir, "spend.jsonl")
	writeFile(t, spend, []byte(`{"request_id":"a","model":"claude-opus-4-1","spend":1.5,"total_tokens":500,"request_tags":["branch:feat/x"]}
{"request_id":"b","model":"ollama/q","model_group":"local","spend":0,"total_tokens":50,"request_tags":["branch:feat/x"]}
{"request_id":"c","model":"claude-opus-4-1","spend":0.5,"total_tokens":5,"request_tags":["branch:feat/o"]}
{"request_id":"d","model":"claude-opus-4-1","spend":0.25,"total_tokens":5}
`))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, SpendLogPath: spend, Milestone: "M1"})
	ms := report.MilestoneSummary
	if ms.AttributedSpend != "$1.50" || ms.OtherUnitsSpend != "$0.50" || ms.UnattributedSpend != "$0.25" || ms.TotalSpend != "$2.25" {
		t.Errorf("spend scopes must add up: %+v", ms)
	}
}

func TestCollector_Boundary_NoGatewayEntries(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	spend := filepath.Join(dir, "spend.jsonl")
	writeFile(t, spend, []byte(""))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, SpendLogPath: spend, Milestone: "M1"})
	u := unitByNumber(t, report, 1)
	if u.FrontierTokens != "1150" || u.LocalFirstRatio != "0.0%" || u.Spend != NotMeasured {
		t.Errorf("transcripts feed tokens without gateway entries: %+v", u)
	}
	if u.Sources != "transcripts" {
		t.Errorf("expected sources transcripts, got %q", u.Sources)
	}
	if u.TranscriptRequestsNotAdded != nil {
		t.Errorf("expected nil TranscriptRequestsNotAdded, got %v", *u.TranscriptRequestsNotAdded)
	}
}

func TestCollector_Negative_NoRequestsYieldsNotMeasuredSources(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, Milestone: "M1"})
	u := unitByNumber(t, report, 2)
	if u.Sources != NotMeasured {
		t.Errorf("expected sources %q, got %q", NotMeasured, u.Sources)
	}
	if u.FrontierTokens != NotMeasured || u.LocalFirstRatio != NotMeasured {
		t.Errorf("expected not measured tokens and local-first: %+v", u)
	}
}

func TestCollector_Positive_TranscriptsFeedUnitsWithoutGateway(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, Milestone: "M1"})
	u := unitByNumber(t, report, 1)
	if u.FrontierTokens != "1150" || u.LocalFirstRatio != "0.0%" || u.OperatorTouches != "2" {
		t.Errorf("%+v", u)
	}
	if want := ">= 0.67 per qualified unit (lower bound: total 2 over 1 of 3 units measured / 3 qualified)"; report.MilestoneSummary.OperatorTouches != want {
		t.Errorf("summary: %q", report.MilestoneSummary.OperatorTouches)
	}
}

func TestCollector_Boundary_ReusedBranchJoinsOnlyTheLatestMergedPR(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(`[
 {"number":1,"head_branch":"feat/x","title":"PR 1","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-01T10:00:00Z","merged_at":"2026-10-02T10:00:00Z",
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}},
 {"number":2,"head_branch":"feat/x","title":"PR 2","disposition":"qualified","metric_epoch":"2026-10-10",
  "created_at":"2026-10-03T10:00:00Z","merged_at":"2026-10-04T10:00:00Z",
  "tokens_by_provider":{"value":{},"provenance":"measured"},
  "wall_seconds":{"value":100,"provenance":"measured"},
  "review_rounds":{"value":1,"provenance":"measured"},
  "retries":{"value":0,"provenance":"measured"},
  "operator_minutes":{"value":1.0,"provenance":"measured"},
  "escaped_defects":{"value":0,"provenance":"measured"}}
]`))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr})
	if unitByNumber(t, report, 1).OperatorTouches != NotMeasured || unitByNumber(t, report, 2).OperatorTouches != "2" {
		t.Errorf("sessions counted for the wrong or both pull requests: %+v", report.Units)
	}
}

func TestFormatSpend_Boundary_SubCentIsNotZero(t *testing.T) {
	if formatSpend(0.0042) != "$0.0042" || formatSpend(0) != "$0.00" || formatSpend(1.5) != "$1.50" {
		t.Errorf("%s %s %s", formatSpend(0.0042), formatSpend(0), formatSpend(1.5))
	}
	if formatDuration(30e9) != "30s" || formatDuration(90*60e9) != "1h30m" {
		t.Error("duration format")
	}
}

func TestRenderTable_Positive_PrintsNotes(t *testing.T) {
	report := newReport("")
	report.Notes = []string{"forge listing incomplete: x"}
	var out bytes.Buffer
	if err := RenderTable(report, &out); err != nil || !strings.Contains(out.String(), "- forge listing incomplete: x") {
		t.Fatalf("%v %q", err, out.String())
	}
}

// twoQualifiedOneRevertedRecords is the acceptance fixture of #1132: two qualified units and
// one reverted unit.
const twoQualifiedOneRevertedRecords = `[
  {
    "number": 1,
    "head_branch": "feat/first",
    "title": "First qualified unit",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {"anthropic": 1000}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 5.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 2,
    "head_branch": "feat/second",
    "title": "Second qualified unit",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {"anthropic": 2000}, "provenance": "measured"},
    "wall_seconds": {"value": 200, "provenance": "measured"},
    "review_rounds": {"value": 3, "provenance": "measured"},
    "retries": {"value": 2, "provenance": "measured"},
    "operator_minutes": {"value": 15.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 3,
    "head_branch": "feat/third",
    "title": "Third reverted unit",
    "disposition": "reverted",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {"anthropic": 400}, "provenance": "measured"},
    "wall_seconds": {"value": 60, "provenance": "measured"},
    "review_rounds": {"value": 2, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 4.0, "provenance": "measured"},
    "escaped_defects": {"value": 1, "provenance": "measured"}
  }
]`

func TestCollector_Positive_QualifiedDenominatorAndLaneCounts(t *testing.T) {
	// Acceptance: A fixture with two qualified units and one reverted unit reports the revert in the lane counts and divides by 2.
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(twoQualifiedOneRevertedRecords))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	if len(report.Units) != 3 {
		t.Fatalf("expected 3 units, got %d", len(report.Units))
	}
	ms := report.MilestoneSummary

	t.Run("LaneCounts", func(t *testing.T) {
		if ms.QualifiedUnits != 2 {
			t.Errorf("expected 2 qualified units, got %d", ms.QualifiedUnits)
		}
		if ms.LaneCounts != (LaneCounts{Qualified: 2, Reverted: 1, Total: 3}) {
			t.Errorf("unexpected lane counts: %+v", ms.LaneCounts)
		}
		laneStr := ms.LaneCounts.String()
		if !strings.Contains(laneStr, "1 reverted") || !strings.Contains(laneStr, "2 qualified") {
			t.Errorf("lane counts string must report revert: %q", laneStr)
		}
	})

	t.Run("AveragedRates", func(t *testing.T) {
		assertNumericAverages(t, ms)
		assertDefectsAndTokens(t, ms)
	})

	t.Run("RenderTable", func(t *testing.T) {
		var tableBuf bytes.Buffer
		if err := RenderTable(report, &tableBuf); err != nil {
			t.Fatalf("render table: %v", err)
		}
		if !strings.Contains(tableBuf.String(), "1 reverted") {
			t.Errorf("table output must report revert in lane counts:\n%s", tableBuf.String())
		}
	})
}

// perQualified returns the per-qualified-unit rate of one vector component.
func perQualified(t *testing.T, name string, v VectorSummary, key string) float64 {
	t.Helper()
	for _, c := range v.Components {
		if c.Key == key && c.PerQualifiedUnit != nil {
			return *c.PerQualifiedUnit
		}
	}
	t.Fatalf("%s: no per-qualified component %q in %+v", name, key, v)
	return 0
}

func assertNumericAverages(t *testing.T, ms MilestoneSummary) {
	t.Helper()
	for name, c := range map[string]struct {
		v    VectorSummary
		want float64
	}{
		"wall seconds (360/2)":    {ms.WallSeconds, 180},
		"review rounds (6/2)":     {ms.ReviewRounds, 3},
		"retries (2/2)":           {ms.Retries, 1},
		"operator minutes (24/2)": {ms.OperatorMinutes, 12},
		"escaped defects (1/2)":   {ms.EscapedDefects, 0.5},
	} {
		if got := perQualified(t, name, c.v, ""); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func assertDefectsAndTokens(t *testing.T, ms MilestoneSummary) {
	t.Helper()
	if got := perQualified(t, "tokens", ms.TokensByProvider, "anthropic"); got != 1700.0 {
		t.Errorf("expected anthropic token rate 1700.0 (3400/2), got %f", got)
	}
	if ms.EscapedDefects.Components[0].Total != 1 {
		t.Errorf("expected 1 defect in total, got %+v", ms.EscapedDefects)
	}
	if !strings.HasPrefix(ms.EscapedDefects.Display, "0.50 per qualified unit (total 1 over 3 units / 2 qualified)") {
		t.Errorf("expected defect rate divided by 2 with the rule named, got %q", ms.EscapedDefects.Display)
	}
}

func TestCollector_Positive_ZeroFailureRuleOfThreeBound(t *testing.T) {
	// Honest edge case: a zero-failure claim prints its n and the rule-of-three bound
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/a",
    "title": "Unit A",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 10, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 2,
    "head_branch": "feat/b",
    "title": "Unit B",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 10, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	ms := report.MilestoneSummary
	expectedClaim := "0 (rule-of-three bound <= 1.50 per qualified unit; n=2 qualified units, 2 units observed) [measured 2]"
	if ms.EscapedDefects.Display != expectedClaim {
		t.Errorf("zero-failure claim must print n and rule-of-three bound, want %q, got %q", expectedClaim, ms.EscapedDefects.Display)
	}
}

func TestCollector_Boundary_ZeroQualifiedUnitsPrintsUndefinedNeverZero(t *testing.T) {
	// Honest edge case: zero qualified units prints undefined, never 0
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/rev",
    "title": "Reverted unit only",
    "disposition": "reverted",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 50, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	ms := report.MilestoneSummary
	if ms.QualifiedUnits != 0 {
		t.Fatalf("expected 0 qualified units, got %d", ms.QualifiedUnits)
	}
	rates := []struct {
		name string
		val  string
	}{
		{"AvgIssueToMerge", ms.AvgIssueToMerge},
		{"WallSeconds", ms.WallSeconds.Display},
		{"ReviewRounds", ms.ReviewRounds.Display},
		{"Retries", ms.Retries.Display},
		{"OperatorMinutes", ms.OperatorMinutes.Display},
		{"EscapedDefects", ms.EscapedDefects.Display},
		{"TokensByProvider", ms.TokensByProvider.Display},
		{"OperatorTouches", ms.OperatorTouches},
		{"FrontierTokens", ms.FrontierTokens},
		{"SpendPerQualifiedUnit", ms.SpendPerQualifiedUnit},
	}
	for _, r := range rates {
		if r.val != UndefinedRate {
			t.Errorf("rate %s with 0 qualified units must be %q, got %q", r.name, UndefinedRate, r.val)
		}
	}
	// Ratios pool usage, not qualified units: with no session source they are not measured.
	if ms.PromptCacheHitRate != NotMeasured || ms.LocalFirstRatio != NotMeasured {
		t.Errorf("ratios without a usage source: cache %q local %q", ms.PromptCacheHitRate, ms.LocalFirstRatio)
	}
	if got := ms.WallSeconds.Components; len(got) != 1 || got[0].Total != 50 || got[0].PerQualifiedUnit != nil {
		t.Errorf("the total stays visible while the per-qualified rate is undefined: %+v", got)
	}
}

func TestCollector_Negative_RowWithoutProvenanceRefused(t *testing.T) {
	// Acceptance: A row without provenance labels is refused.
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/no-prov",
    "title": "Missing provenance",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: prsPath}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected Collect to fail and refuse row without provenance labels")
	}
	if !strings.Contains(err.Error(), "missing or invalid provenance label") {
		t.Errorf("expected error to name missing provenance label, got: %v", err)
	}
}

func TestCollector_Negative_InvalidProvenanceRefused(t *testing.T) {
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/bad-prov",
    "title": "Bad provenance",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "wall_seconds": {"value": 100, "provenance": "guessed"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"},
    "tokens_by_provider": {"value": {}, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: prsPath}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected Collect to fail and refuse row with invalid provenance label")
	}
	if !strings.Contains(err.Error(), "missing or invalid provenance label") {
		t.Errorf("expected error to name provenance, got: %v", err)
	}
}

func TestCollector_Negative_MissingMetricEpochRefused(t *testing.T) {
	row := UnitReport{
		PullRequestNumber: 1,
		Disposition:       DispositionQualified,
		TokensByProvider:  &VectorField[map[string]int64]{Provenance: ProvenanceMeasured},
		WallSeconds:       &VectorField[int64]{Provenance: ProvenanceMeasured},
		ReviewRounds:      &VectorField[int]{Provenance: ProvenanceMeasured},
		Retries:           &VectorField[int]{Provenance: ProvenanceMeasured},
		OperatorMinutes:   &VectorField[float64]{Provenance: ProvenanceMeasured},
		EscapedDefects:    &VectorField[int]{Provenance: ProvenanceMeasured},
	}
	if err := ValidateRow(row); err == nil {
		t.Fatal("expected ValidateRow to refuse row with missing metric_epoch")
	}
}

func TestCollector_Negative_MixedMetricEpochsRefused(t *testing.T) {
	row1 := UnitReport{
		PullRequestNumber: 1,
		MetricEpoch:       CurrentMetricEpoch,
		Disposition:       DispositionQualified,
		TokensByProvider:  &VectorField[map[string]int64]{Provenance: ProvenanceMeasured},
		WallSeconds:       &VectorField[int64]{Provenance: ProvenanceMeasured},
		ReviewRounds:      &VectorField[int]{Provenance: ProvenanceMeasured},
		Retries:           &VectorField[int]{Provenance: ProvenanceMeasured},
		OperatorMinutes:   &VectorField[float64]{Provenance: ProvenanceMeasured},
		EscapedDefects:    &VectorField[int]{Provenance: ProvenanceMeasured},
	}
	row2 := row1
	row2.PullRequestNumber = 2
	row2.MetricEpoch = "2025-01-01"

	err := ValidateRows([]UnitReport{row1, row2})
	if err == nil {
		t.Fatal("expected ValidateRows to refuse mixed metric epochs")
	}
	if !strings.Contains(err.Error(), "mixed metric epochs in ledger") {
		t.Errorf("expected mixed epochs error, got: %v", err)
	}
}

func TestCollector_Negative_RowCarryingOnlyNumberBranchTitleRefused(t *testing.T) {
	// Blocker 1: A row with no provenance labels must be refused through the collector path.
	fixture := `[{"number": 1, "head_branch": "feat/bare", "title": "Bare PR"}]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: prsPath}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected Collect to refuse row carrying only number, branch, and title")
	}
}

func TestCollector_Negative_MissingMetricEpochRefusedCollectorPath(t *testing.T) {
	// Major 2: Missing metric_epoch refused through collector path
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/no-epoch",
    "title": "No Epoch PR",
    "disposition": "qualified",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: prsPath}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected Collect to refuse row missing metric_epoch through collector path")
	}
	if !strings.Contains(err.Error(), "missing metric_epoch tag") {
		t.Errorf("expected error to name missing metric_epoch tag, got: %v", err)
	}
}

func TestCollector_Positive_ProbeQualifiedAndRevertedUnitRates(t *testing.T) {
	// Major 3 probe: 1 qualified + 1 reverted must print '1 of 1 units measured' not '2 of 2'.
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/qual",
    "title": "Qualified PR",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-01T10:00:00Z",
    "merged_at": "2026-10-01T12:00:00Z",
    "closing_issues": [{"number": 10, "created_at": "2026-10-01T08:00:00Z"}],
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 2,
    "head_branch": "feat/rev",
    "title": "Reverted PR",
    "disposition": "reverted",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-02T10:00:00Z",
    "merged_at": "2026-10-02T12:00:00Z",
    "closing_issues": [{"number": 11, "created_at": "2026-10-02T08:00:00Z"}],
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	ms := report.MilestoneSummary
	if ms.QualifiedUnits != 1 {
		t.Fatalf("expected 1 qualified unit, got %d", ms.QualifiedUnits)
	}
	if !strings.Contains(ms.AvgIssueToMerge, "mean over 1 of 1 qualified units measured") {
		t.Errorf("probe failure: expected 'mean over 1 of 1 qualified units measured', got %q", ms.AvgIssueToMerge)
	}
	if strings.Contains(ms.AvgIssueToMerge, "2 of 2") {
		t.Errorf("probe failure: reverted unit must not be in rate denominator: %q", ms.AvgIssueToMerge)
	}
}

func TestCollector_Positive_RevertRuleMarksOriginalReverted(t *testing.T) {
	// Major 5 positive: Revert PR marks the original PR as reverted
	fixture := `[
  {
    "number": 10,
    "head_branch": "feat/orig",
    "title": "Add super feature",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-01T10:00:00Z",
    "merged_at": "2026-10-01T12:00:00Z",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 11,
    "head_branch": "revert-10",
    "title": "Revert \"Add super feature\"",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-02T10:00:00Z",
    "merged_at": "2026-10-02T12:00:00Z",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 50, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	orig := unitByNumber(t, report, 10)
	rev := unitByNumber(t, report, 11)
	if orig.Disposition != DispositionReverted {
		t.Errorf("original PR #10 must be marked reverted, got %q", orig.Disposition)
	}
	if rev.Disposition != DispositionQualified {
		t.Errorf("revert PR #11 itself must be qualified, got %q", rev.Disposition)
	}
}

func TestCollector_Negative_RevertRuleNonMatchingTitle(t *testing.T) {
	// Major 5 negative: non-revert titles do not change disposition
	fixture := `[
  {
    "number": 20,
    "head_branch": "feat/first",
    "title": "Add widget A",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-01T10:00:00Z",
    "merged_at": "2026-10-01T12:00:00Z",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 21,
    "head_branch": "feat/second",
    "title": "Add widget B",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "created_at": "2026-10-02T10:00:00Z",
    "merged_at": "2026-10-02T12:00:00Z",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	u1 := unitByNumber(t, report, 20)
	u2 := unitByNumber(t, report, 21)
	if u1.Disposition != DispositionQualified || u2.Disposition != DispositionQualified {
		t.Errorf("neither PR is a revert; both must be qualified: u1=%q, u2=%q", u1.Disposition, u2.Disposition)
	}
}

// perLaneRecords spreads three units over two lanes.
const perLaneRecords = `[
  {
    "number": 1,
    "head_branch": "feat/l1",
    "title": "Unit 1",
    "lane": "fast-lane",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 2,
    "head_branch": "feat/l2",
    "title": "Unit 2",
    "lane": "fast-lane",
    "disposition": "reverted",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  },
  {
    "number": 3,
    "head_branch": "feat/l3",
    "title": "Unit 3",
    "lane": "batch-lane",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "tokens_by_provider": {"value": {}, "provenance": "measured"},
    "wall_seconds": {"value": 100, "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"}
  }
]`

func TestCollector_Positive_PerLaneAggregation(t *testing.T) {
	// Major 4: Per-lane aggregation and table rendering
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(perLaneRecords))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	ms := report.MilestoneSummary
	if len(ms.PerLane) != 2 {
		t.Fatalf("expected 2 lanes, got %d", len(ms.PerLane))
	}
	fast := ms.PerLane["fast-lane"]
	if fast.Qualified != 1 || fast.Reverted != 1 || fast.Total != 2 {
		t.Errorf("unexpected fast-lane counts: %+v", fast)
	}
	batch := ms.PerLane["batch-lane"]
	if batch.Qualified != 1 || batch.Total != 1 {
		t.Errorf("unexpected batch-lane counts: %+v", batch)
	}

	var buf bytes.Buffer
	if err := RenderTable(report, &buf); err != nil {
		t.Fatalf("render table: %v", err)
	}
	tableOut := buf.String()
	if !strings.Contains(tableOut, "Lane Counts (batch-lane):") || !strings.Contains(tableOut, "Lane Counts (fast-lane):") {
		t.Errorf("table output must render per-lane counts:\n%s", tableOut)
	}
}

func TestCollector_Negative_UnmarshalVectorTypeError(t *testing.T) {
	// Major 6: Vector unmarshal error reporting with context
	fixture := `[
  {
    "number": 1,
    "head_branch": "feat/bad-type",
    "title": "Bad Type PR",
    "disposition": "qualified",
    "metric_epoch": "2026-10-10",
    "wall_seconds": {"value": "not-an-int", "provenance": "measured"},
    "review_rounds": {"value": 1, "provenance": "measured"},
    "retries": {"value": 0, "provenance": "measured"},
    "operator_minutes": {"value": 1.0, "provenance": "measured"},
    "escaped_defects": {"value": 0, "provenance": "measured"},
    "tokens_by_provider": {"value": {}, "provenance": "measured"}
  }
]`
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: prsPath}).Collect(context.Background())
	if err == nil {
		t.Fatal("expected Collect to fail on vector field unmarshal type error")
	}
	if !strings.Contains(err.Error(), `field "wall_seconds": value:`) {
		t.Errorf("expected error to wrap field name with context, got: %v", err)
	}
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("the underlying json error must survive the wrapping: %v", err)
	}
}

func TestFormatZeroFailureClaim_PositiveAndBoundary(t *testing.T) {
	for _, c := range []struct {
		qualified, observed int
		want                string
	}{
		{0, 3, UndefinedRate},
		{1, 1, "0 (rule-of-three bound <= 3.00 per qualified unit; n=1 qualified units, 1 units observed)"},
		{2, 3, "0 (rule-of-three bound <= 1.50 per qualified unit; n=2 qualified units, 3 units observed)"},
		{10, 10, "0 (rule-of-three bound <= 0.30 per qualified unit; n=10 qualified units, 10 units observed)"},
		{100, 120, "0 (rule-of-three bound <= 0.03 per qualified unit; n=100 qualified units, 120 units observed)"},
	} {
		if got := FormatZeroFailureClaim(c.qualified, c.observed); got != c.want {
			t.Errorf("qualified %d observed %d: got %q, want %q", c.qualified, c.observed, got, c.want)
		}
	}
}

// Offered is one disposition among six; Total counts every unit.
func TestLaneCounts_PositiveAndBoundary(t *testing.T) {
	var lc LaneCounts
	for _, d := range []string{DispositionQualified, DispositionQualified, DispositionReverted, DispositionRejected, DispositionAbandoned, DispositionTimedOut, DispositionOffered} {
		lc.Add(d)
	}
	want := LaneCounts{Qualified: 2, Reverted: 1, Rejected: 1, Abandoned: 1, TimedOut: 1, Offered: 1, Total: 7}
	if lc != want {
		t.Errorf("counts %+v, want %+v", lc, want)
	}
	if got := lc.String(); got != "2 qualified, 1 reverted, 1 rejected, 1 abandoned, 1 timed_out, 1 offered, 7 total" {
		t.Errorf("unexpected string: %q", got)
	}
	var empty LaneCounts
	if got := empty.String(); got != "0 qualified, 0 total" {
		t.Errorf("empty lane counts: %q", got)
	}
}

func TestCollector_Positive_OutcomesPopulateUnitAndSummary(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	outcomesPath := filepath.Join(dir, "outcomes.jsonl")

	o1 := router.Outcome{
		Time:       time.Now().UTC(),
		Task:       "coding",
		Target:     "claude-3-7-sonnet",
		Branch:     "feat/x",
		Result:     router.OutcomeOK,
		DurationMS: 5000,
		ActualCost: ptrFloat64(0.015),
		Identity: router.RunIdentity{
			PhysicalModel:  "claude-3-7-sonnet-20250219",
			Harness:        "claude-code",
			HarnessVersion: "1.0.0",
			PromptDigest:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ContextDigest:  "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			ContextBytes:   4096,
			ToolSet:        []string{"bash", "view"},
			CostEstimate:   ptrFloat64(0.010),
		},
	}
	o2 := router.Outcome{
		Time:       time.Now().UTC(),
		Task:       "fast",
		Target:     "claude-3-5-haiku",
		Branch:     "feat/z",
		Result:     router.OutcomeOK,
		DurationMS: 2000,
		Identity: router.RunIdentity{
			PhysicalModel:  "claude-3-5-haiku-20241022",
			Harness:        "claude-code",
			HarnessVersion: "1.0.0",
			PromptDigest:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			ContextDigest:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
			ContextBytes:   1024,
			ToolSet:        []string{"view"},
			CostEstimate:   ptrFloat64(0.005),
		},
	}
	if err := router.AppendOutcome(context.Background(), outcomesPath, o1); err != nil {
		t.Fatal(err)
	}
	if err := router.AppendOutcome(context.Background(), outcomesPath, o2); err != nil {
		t.Fatal(err)
	}

	report := collect(t, CollectorOptions{ForgeJSONPath: prs, OutcomeLogPath: outcomesPath, Milestone: "M1"})

	// PR #1 has measured actual cost
	u1 := unitByNumber(t, report, 1)
	if u1.ResolvedModel != "claude-3-7-sonnet-20250219" {
		t.Errorf("u1.ResolvedModel = %q, want claude-3-7-sonnet-20250219", u1.ResolvedModel)
	}
	if u1.Identity == nil || u1.Identity.PhysicalModel != "claude-3-7-sonnet-20250219" {
		t.Fatalf("u1.Identity = %+v, want non-nil with PhysicalModel", u1.Identity)
	}
	if u1.IdentityKey == "" {
		t.Errorf("u1.IdentityKey is empty")
	}
	if u1.EstimateError != "$+0.0050" {
		t.Errorf("u1.EstimateError = %q, want $+0.0050", u1.EstimateError)
	}
	if u1.EstimateErrorAmount == nil || math.Abs(*u1.EstimateErrorAmount-0.005) > 1e-6 {
		t.Errorf("u1.EstimateErrorAmount = %v, want 0.005", u1.EstimateErrorAmount)
	}

	// PR #2 has no outcome
	u2 := unitByNumber(t, report, 2)
	if u2.ResolvedModel != "" || u2.Identity != nil || u2.IdentityKey != "" {
		t.Errorf("u2 should have empty identity fields: %+v", u2)
	}
	if u2.EstimateError != NotMeasured || u2.EstimateErrorAmount != nil {
		t.Errorf("u2.EstimateError = %q, want NotMeasured", u2.EstimateError)
	}

	// PR #3 has outcome but unmeasured actual cost
	u3 := unitByNumber(t, report, 3)
	if u3.ResolvedModel != "claude-3-5-haiku-20241022" {
		t.Errorf("u3.ResolvedModel = %q, want claude-3-5-haiku-20241022", u3.ResolvedModel)
	}
	if u3.Identity == nil {
		t.Errorf("u3.Identity should be populated")
	}
	if u3.EstimateError != NotMeasured || u3.EstimateErrorAmount != nil {
		t.Errorf("u3.EstimateError = %q, want NotMeasured", u3.EstimateError)
	}

	// MilestoneSummary aggregates measured units
	if report.MilestoneSummary.EstimateError != "$+0.0050" {
		t.Errorf("summary.EstimateError = %q, want $+0.0050", report.MilestoneSummary.EstimateError)
	}
	if report.MilestoneSummary.EstimateErrorAmount == nil || math.Abs(*report.MilestoneSummary.EstimateErrorAmount-0.005) > 1e-6 {
		t.Errorf("summary.EstimateErrorAmount = %v, want 0.005", report.MilestoneSummary.EstimateErrorAmount)
	}
}
