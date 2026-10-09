// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"context"
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
	if ms.UnitsCount != 0 || ms.LocalFirstRatio != NotMeasured || ms.PromptCacheHitRate != NotMeasured || ms.AvgIssueToMerge != NotMeasured {
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
