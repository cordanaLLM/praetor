// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

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
 {"number":1,"head_branch":"feat/x","title":"One","milestone":"M1","created_at":"2026-10-01T10:00:00Z","merged_at":"2026-10-04T10:00:00Z",
  "closing_issues":[{"number":10,"created_at":"2026-10-01T00:00:00Z"}]},
 {"number":2,"head_branch":"feat/y","title":"Two","milestone":"M1","created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-03T10:00:00Z",
  "closing_issues":[{"number":11}]},
 {"number":3,"head_branch":"feat/z","title":"Three","milestone":"M1","created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-02T10:00:00Z"},
 {"number":4,"head_branch":"feat/o","title":"Other","milestone":"M2","created_at":"2026-10-02T10:00:00Z","merged_at":"2026-10-09T10:00:00Z"}
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
	if !strings.Contains(report.MilestoneSummary.AvgIssueToMerge, "3d10h (1 of 3 units measured)") {
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
		PullRequests: []forge.MergedPullRequest{{Number: 7, HeadBranch: "b"}},
		Truncated:    "scanned only 2000",
		Warnings:     []string{"closing issue #9 not fetched"},
	}}
	report := collect(t, CollectorOptions{ForgeDriver: stub, Milestone: "M1", Limit: 5, Notes: []string{"caller note"}})
	if stub.query.Limit != 5 || stub.query.Milestone != "M1" {
		t.Errorf("milestone and limit go to the forge together: %+v", stub.query)
	}
	joined := strings.Join(report.Notes, "\n")
	for _, want := range []string{"caller note", "forge listing incomplete: scanned only 2000", "forge: closing issue #9 not fetched"} {
		if !strings.Contains(joined, want) {
			t.Errorf("note %q missing in %q", want, joined)
		}
	}
	if !report.Sources.Forge || len(report.Units) != 1 {
		t.Errorf("%+v", report)
	}
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
	if !strings.Contains(report.MilestoneSummary.OperatorTouches, "2 (1 of 3 units measured)") {
		t.Errorf("summary: %q", report.MilestoneSummary.OperatorTouches)
	}
}

func TestCollector_Boundary_ReusedBranchJoinsOnlyTheLatestMergedPR(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(`[
 {"number":1,"head_branch":"feat/x","created_at":"2026-10-01T10:00:00Z","merged_at":"2026-10-02T10:00:00Z"},
 {"number":2,"head_branch":"feat/x","created_at":"2026-10-03T10:00:00Z","merged_at":"2026-10-04T10:00:00Z"}]`))
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

func TestCollector_Positive_QualifiedDenominatorAndLaneCounts(t *testing.T) {
	// Acceptance: A fixture with two qualified units and one reverted unit reports the revert in the lane counts and divides by 2.
	fixture := `[
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
	prsPath := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, prsPath, []byte(fixture))
	report := collect(t, CollectorOptions{ForgeJSONPath: prsPath})

	if len(report.Units) != 3 {
		t.Fatalf("expected 3 units, got %d", len(report.Units))
	}
	ms := report.MilestoneSummary
	if ms.QualifiedUnits != 2 {
		t.Errorf("expected 2 qualified units, got %d", ms.QualifiedUnits)
	}
	if ms.LaneCounts.Qualified != 2 || ms.LaneCounts.Reverted != 1 || ms.LaneCounts.Offered != 3 {
		t.Errorf("unexpected lane counts: %+v", ms.LaneCounts)
	}
	laneStr := ms.LaneCounts.String()
	if !strings.Contains(laneStr, "1 reverted") || !strings.Contains(laneStr, "2 qualified") {
		t.Errorf("lane counts string must report revert: %q", laneStr)
	}

	// Verify denominator is 2 (divides by 2):
	// Wall seconds: (100 + 200 + 60) / 2 = 180s = 3m
	if ms.AvgWallSecondsNum == nil || *ms.AvgWallSecondsNum != 180.0 {
		t.Errorf("expected avg wall seconds 180 (360/2), got %v", ms.AvgWallSecondsNum)
	}
	// Review rounds: (1 + 3 + 2) / 2 = 3.00
	if ms.AvgReviewRoundsNum == nil || *ms.AvgReviewRoundsNum != 3.0 {
		t.Errorf("expected avg review rounds 3.0 (6/2), got %v", ms.AvgReviewRoundsNum)
	}
	// Retries: (0 + 2 + 0) / 2 = 1.00
	if ms.AvgRetriesNum == nil || *ms.AvgRetriesNum != 1.0 {
		t.Errorf("expected avg retries 1.0 (2/2), got %v", ms.AvgRetriesNum)
	}
	// Operator minutes: (5.0 + 15.0 + 4.0) / 2 = 12.0m
	if ms.AvgOperatorMinutesNum == nil || *ms.AvgOperatorMinutesNum != 12.0 {
		t.Errorf("expected avg operator minutes 12.0 (24/2), got %v", ms.AvgOperatorMinutesNum)
	}
	// Tokens by provider: anthropic (1000 + 2000 + 400) / 2 = 1700.0/unit
	if ms.TokensByProvider["anthropic"] != 1700.0 {
		t.Errorf("expected anthropic token rate 1700.0 (3400/2), got %f", ms.TokensByProvider["anthropic"])
	}
	// Escaped defects: 1 defect across 2 qualified units = 0.50 per qualified unit
	if ms.EscapedDefectsNum == nil || *ms.EscapedDefectsNum != 1 {
		t.Errorf("expected 1 defect, got %v", ms.EscapedDefectsNum)
	}
	if !strings.Contains(ms.EscapedDefects, "0.50 per qualified unit") {
		t.Errorf("expected defect rate divided by 2, got %q", ms.EscapedDefects)
	}

	var tableBuf bytes.Buffer
	if err := RenderTable(report, &tableBuf); err != nil {
		t.Fatalf("render table: %v", err)
	}
	tableOut := tableBuf.String()
	if !strings.Contains(tableOut, "1 reverted") {
		t.Errorf("table output must report revert in lane counts:\n%s", tableOut)
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
	expectedClaim := "0 (n=2, rule-of-three bound <= 1.50)"
	if ms.EscapedDefects != expectedClaim {
		t.Errorf("zero-failure claim must print n and rule-of-three bound, want %q, got %q", expectedClaim, ms.EscapedDefects)
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
		{"AvgWallSeconds", ms.AvgWallSeconds},
		{"AvgReviewRounds", ms.AvgReviewRounds},
		{"AvgRetries", ms.AvgRetries},
		{"AvgOperatorMinutes", ms.AvgOperatorMinutes},
		{"EscapedDefects", ms.EscapedDefects},
		{"EscapedDefectsRate", ms.EscapedDefectsRate},
		{"OperatorTouches", ms.OperatorTouches},
		{"FrontierTokens", ms.FrontierTokens},
		{"PromptCacheHitRate", ms.PromptCacheHitRate},
		{"LocalFirstRatio", ms.LocalFirstRatio},
	}
	for _, r := range rates {
		if r.val != UndefinedRate {
			t.Errorf("rate %s with 0 qualified units must be %q, got %q", r.name, UndefinedRate, r.val)
		}
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
    "wall_seconds": {"value": 100}
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
		TokensByProvider:  VectorField[map[string]int64]{Provenance: ProvenanceMeasured},
		WallSeconds:       VectorField[int64]{Provenance: ProvenanceMeasured},
		ReviewRounds:      VectorField[int]{Provenance: ProvenanceMeasured},
		Retries:           VectorField[int]{Provenance: ProvenanceMeasured},
		OperatorMinutes:   VectorField[float64]{Provenance: ProvenanceMeasured},
		EscapedDefects:    VectorField[int]{Provenance: ProvenanceMeasured},
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
		TokensByProvider:  VectorField[map[string]int64]{Provenance: ProvenanceMeasured},
		WallSeconds:       VectorField[int64]{Provenance: ProvenanceMeasured},
		ReviewRounds:      VectorField[int]{Provenance: ProvenanceMeasured},
		Retries:           VectorField[int]{Provenance: ProvenanceMeasured},
		OperatorMinutes:   VectorField[float64]{Provenance: ProvenanceMeasured},
		EscapedDefects:    VectorField[int]{Provenance: ProvenanceMeasured},
	}
	row2 := row1
	row2.PullRequestNumber = 2
	row2.MetricEpoch = "2025-01-01"

	if err := ValidateRows([]UnitReport{row1, row2}); err == nil {
		t.Fatal("expected ValidateRows to refuse mixed metric epochs")
	}
}

func TestFormatZeroFailureClaim_PositiveAndBoundary(t *testing.T) {
	if got := FormatZeroFailureClaim(0); got != UndefinedRate {
		t.Errorf("n=0 must be undefined, got %q", got)
	}
	if got := FormatZeroFailureClaim(1); got != "0 (n=1, rule-of-three bound <= 3.00)" {
		t.Errorf("n=1 got %q", got)
	}
	if got := FormatZeroFailureClaim(2); got != "0 (n=2, rule-of-three bound <= 1.50)" {
		t.Errorf("n=2 got %q", got)
	}
	if got := FormatZeroFailureClaim(10); got != "0 (n=10, rule-of-three bound <= 0.30)" {
		t.Errorf("n=10 got %q", got)
	}
	if got := FormatZeroFailureClaim(100); got != "0 (n=100, rule-of-three bound <= 0.03)" {
		t.Errorf("n=100 got %q", got)
	}
}

func TestLaneCounts_PositiveAndBoundary(t *testing.T) {
	var lc LaneCounts
	lc.Add(DispositionQualified)
	lc.Add(DispositionQualified)
	lc.Add(DispositionReverted)
	lc.Add(DispositionRejected)
	lc.Add(DispositionAbandoned)
	lc.Add(DispositionTimedOut)
	lc.Add(DispositionOffered)

	if lc.Qualified != 2 || lc.Reverted != 1 || lc.Rejected != 1 || lc.Abandoned != 1 || lc.TimedOut != 1 || lc.Offered != 7 {
		t.Errorf("unexpected counts: %+v", lc)
	}
	str := lc.String()
	if !strings.Contains(str, "2 qualified") || !strings.Contains(str, "1 reverted") || !strings.Contains(str, "7 offered") {
		t.Errorf("unexpected string: %q", str)
	}
}

func TestVectorField_UnmarshalJSON_PositiveAndBoundary(t *testing.T) {
	// Full object
	var f1 VectorField[int64]
	if err := json.Unmarshal([]byte(`{"value": 42, "provenance": "measured"}`), &f1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f1.Value != 42 || f1.Provenance != ProvenanceMeasured {
		t.Errorf("f1: %+v", f1)
	}

	// Missing provenance
	var f2 VectorField[int64]
	if err := json.Unmarshal([]byte(`{"value": 42}`), &f2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f2.Provenance != "" {
		t.Errorf("expected empty provenance, got %q", f2.Provenance)
	}

	// Raw number
	var f3 VectorField[int64]
	if err := json.Unmarshal([]byte(`42`), &f3); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f3.Value != 42 || f3.Provenance != "" {
		t.Errorf("f3: %+v", f3)
	}
}
