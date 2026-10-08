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

func TestCollector_Positive_GatewayWinsOverTranscriptsWithoutDoubleCount(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	tr := filepath.Join(dir, "tr")
	writeFile(t, filepath.Join(tr, "s.jsonl"), readFixture(t))
	spend := filepath.Join(dir, "spend.jsonl")
	writeFile(t, spend, []byte(`{"request_id":"a","model":"claude-opus-4-1","spend":1.5,"total_tokens":500,"request_tags":["branch:feat/x"]}
{"request_id":"b","model":"ollama/q","model_group":"local","spend":0,"total_tokens":50,"request_tags":["branch:feat/x"]}
{"request_id":"c","model":"claude-opus-4-1","spend":0.5,"total_tokens":5,"request_tags":["branch:feat/o"]}
{"request_id":"d","model":"claude-opus-4-1","spend":0.25,"total_tokens":5}
`))
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, TranscriptsDir: tr, SpendLogPath: spend, Milestone: "M1"})
	u := unitByNumber(t, report, 1)
	if u.FrontierTokens != "500" || u.LocalFirstRatio != "50.0%" || u.Spend != "$1.50" {
		t.Errorf("gateway entries decide tokens and local-first: %+v", u)
	}
	if u.OperatorTouches != "2" || u.PromptCacheHitRate == NotMeasured {
		t.Errorf("touches and cache hit still come from transcripts: %+v", u)
	}
	ms := report.MilestoneSummary
	if ms.AttributedSpend != "$1.50" || ms.OtherUnitsSpend != "$0.50" || ms.UnattributedSpend != "$0.25" || ms.TotalSpend != "$2.25" {
		t.Errorf("spend scopes must add up: %+v", ms)
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
