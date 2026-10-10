// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// sampleUnit is one qualified unit joined by every source.
func sampleUnit() UnitReport {
	now := time.Now().Truncate(time.Second).UTC()
	spend, touches, tokens := 0.15, 2, int64(500)
	cacheRate, localRate, itmSecs := 0.75, 0.25, int64(7200)
	requests, local := 4, 1
	cacheRead, cacheInput := int64(300), int64(400)
	return UnitReport{
		PullRequestNumber: 101, HeadBranch: "feat/alpha", Title: "Alpha feature", Milestone: "1.0",
		CreatedAt: now.Add(-2 * time.Hour), MergedAt: now,
		IssueToMerge: "2h00m", IssueToMergeSecs: &itmSecs,
		FrontierTokens: "500", FrontierTokensNum: &tokens,
		Spend: "$0.15", SpendAmount: &spend,
		OperatorTouches: "2", OperatorTouchNum: &touches,
		PromptCacheHitRate: "75.0%", CacheHitRatio: &cacheRate, CacheReadTokensNum: &cacheRead, PromptInputTokensNum: &cacheInput,
		LocalFirstRatio: "25.0%", LocalRatio: &localRate, RequestsNum: &requests, LocalRequestsNum: &local,
		FactHitRatio: FollowUpRefs, ChecksBeforeReviews: FollowUpRefs,
		Sources: "transcripts+gateway", Disposition: DispositionQualified, MetricEpoch: CurrentMetricEpoch,
		TokensByProvider: &VectorField[map[string]int64]{Value: map[string]int64{"anthropic": 500}, Provenance: ProvenanceMeasured},
		WallSeconds:      &VectorField[int64]{Value: 7200, Provenance: ProvenanceMeasured},
		ReviewRounds:     &VectorField[int]{Value: 1, Provenance: ProvenanceMeasured},
		Retries:          &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
		OperatorMinutes:  &VectorField[float64]{Value: 15.0, Provenance: ProvenanceMeasured},
		EscapedDefects:   &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
	}
}

// sampleReport builds its summary through buildMilestoneSummary, never by hand.
func sampleReport(t *testing.T) *Report {
	t.Helper()
	report := &Report{
		Units:            []UnitReport{sampleUnit()},
		MilestoneSummary: MilestoneSummary{Milestone: "1.0"},
		Sources:          SourcesMeasured{Forge: true, Transcripts: true, SpendLog: true},
	}
	spend := &SpendReport{SpendByPRNumber: map[int]float64{101: 0.15}, Unattributed: 0.05, TotalSpend: 0.20}
	if err := (&Collector{}).buildMilestoneSummary(report, spend); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestRenderJSON_Positive_Schema(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := RenderJSON(rep, &buf); err != nil {
		t.Fatalf("unexpected error rendering JSON: %v", err)
	}

	jsonBytes := buf.Bytes()
	if !json.Valid(jsonBytes) {
		t.Fatal("output must be valid JSON")
	}

	var parsed Report
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("failed unmarshaling output JSON: %v", err)
	}

	if len(parsed.Units) != 1 || parsed.Units[0].PullRequestNumber != 101 {
		t.Errorf("unexpected units in parsed JSON: %+v", parsed.Units)
	}
	if parsed.Units[0].Sources != "transcripts+gateway" {
		t.Errorf("expected sources transcripts+gateway, got %q", parsed.Units[0].Sources)
	}
	if parsed.MilestoneSummary.Milestone != "1.0" {
		t.Errorf("unexpected milestone in parsed JSON: %s", parsed.MilestoneSummary.Milestone)
	}
	if parsed.MilestoneSummary.FactHitRatio != FollowUpRefs {
		t.Errorf("fact hit ratio must carry follow-up reference in JSON")
	}
}

func TestRenderTable_Positive(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := RenderTable(rep, &buf); err != nil {
		t.Fatalf("unexpected error rendering Table: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ISSUE-TO-MERGE") || !strings.Contains(output, "FRONTIER TOKENS") || !strings.Contains(output, "SOURCES") {
		t.Error("table header missing expected columns")
	}
	if !strings.Contains(output, "#101") || !strings.Contains(output, "feat/alpha") || !strings.Contains(output, "transcripts+gateway") {
		t.Error("table row missing unit data")
	}
	if !strings.Contains(output, "Milestone Summary: 1.0") {
		t.Error("milestone summary section missing")
	}
	if !strings.Contains(output, FollowUpRefs) {
		t.Error("milestone summary missing follow-up reference")
	}
}

func TestRender_Negative_NilReport(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(nil, &buf); err == nil {
		t.Error("expected error rendering nil report as JSON")
	}
	if err := RenderTable(nil, &buf); err == nil {
		t.Error("expected error rendering nil report as Table")
	}
}

func TestRender_Boundary_EmptyReport(t *testing.T) {
	emptyReport := &Report{}
	var buf bytes.Buffer
	if err := RenderJSON(emptyReport, &buf); err != nil {
		t.Fatalf("unexpected error rendering empty report as JSON: %v", err)
	}
	if err := RenderTable(emptyReport, &buf); err != nil {
		t.Fatalf("unexpected error rendering empty report as Table: %v", err)
	}
}

func TestRenderTable_Positive_LabelsNameTheirRule(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderTable(sampleReport(t), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Issue-to-Merge (Qualified Mean):       2h00m (mean over 1 of 1 qualified units measured)",
		"Wall Seconds per Qualified Unit:       2h00m per qualified unit (total 2h00m over 1 units / 1 qualified) [measured 1]",
		"Escaped Defects per Qualified Unit:    0 (rule-of-three bound <= 3.00 per qualified unit; n=1 qualified units, 1 units observed) [measured 1]",
		"Tokens by Provider per Qualified Unit: anthropic: 500.00 per qualified unit (total anthropic: 500 over 1 units / 1 qualified) [measured 1]",
		"Spend per Qualified Unit:              $0.15 per qualified unit (total $0.15 over 1 units / 1 qualified)",
		"Prompt-Cache Hit Rate (All Usage):     75.0% (pooled over the usage of 1 of 1 units)",
		"Local-First Ratio (All Usage):         25.0% (pooled over the usage of 1 of 1 units)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary line missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Avg ") {
		t.Errorf("a total divided by qualified units is not an average and must not be labelled Avg:\n%s", out)
	}
}

func TestRender_Boundary_EmptyLedgerPrintsUndefinedRates(t *testing.T) {
	rep := newReport("M-Empty")
	if err := (&Collector{}).buildMilestoneSummary(rep, nil); err != nil {
		t.Fatal(err)
	}
	assertEmptyLedgerTable(t, rep)
	assertEmptyLedgerJSON(t, rep)
}

func assertEmptyLedgerTable(t *testing.T, rep *Report) {
	t.Helper()
	var tableBuf bytes.Buffer
	if err := RenderTable(rep, &tableBuf); err != nil {
		t.Fatalf("unexpected error rendering table: %v", err)
	}
	tableOut := tableBuf.String()
	for _, label := range []string{
		"Issue-to-Merge (Qualified Mean)", "Wall Seconds per Qualified Unit", "Review Rounds per Qualified Unit",
		"Retries per Qualified Unit", "Operator Minutes per Qualified Unit", "Escaped Defects per Qualified Unit",
		"Tokens by Provider per Qualified Unit", "Operator Touches per Qualified Unit", "Frontier Tokens per Qualified Unit",
		"Spend per Qualified Unit", "Prompt-Cache Hit Rate (All Usage)", "Local-First Ratio (All Usage)",
		"Fact-Hit Ratio", "Checks-Before-Reviews",
	} {
		if want := summaryLine(label, UndefinedRate); !strings.Contains(tableOut, want) {
			t.Errorf("empty ledger table output missing %q, got:\n%s", want, tableOut)
		}
	}
	if want := summaryLine("Lane Counts", "0 qualified, 0 total"); !strings.Contains(tableOut, want) {
		t.Errorf("empty ledger lane counts missing %q:\n%s", want, tableOut)
	}
}

func assertEmptyLedgerJSON(t *testing.T, rep *Report) {
	t.Helper()
	var jsonBuf bytes.Buffer
	if err := RenderJSON(rep, &jsonBuf); err != nil {
		t.Fatalf("unexpected error rendering json: %v", err)
	}
	var parsed Report
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed unmarshaling empty ledger JSON: %v", err)
	}
	ms := parsed.MilestoneSummary
	for name, got := range map[string]string{
		"avg_issue_to_merge": ms.AvgIssueToMerge, "wall_seconds": ms.WallSeconds.Display, "review_rounds": ms.ReviewRounds.Display,
		"retries": ms.Retries.Display, "operator_minutes": ms.OperatorMinutes.Display, "escaped_defects": ms.EscapedDefects.Display,
		"tokens_by_provider": ms.TokensByProvider.Display, "operator_touches": ms.OperatorTouches, "frontier_tokens": ms.FrontierTokens,
		"spend_per_qualified_unit": ms.SpendPerQualifiedUnit, "prompt_cache_hit_rate": ms.PromptCacheHitRate,
		"local_first_ratio": ms.LocalFirstRatio, "fact_hit_ratio": ms.FactHitRatio, "checks_before_reviews": ms.ChecksBeforeReviews,
	} {
		if got != UndefinedRate {
			t.Errorf("empty ledger JSON %s = %q, want %q", name, got, UndefinedRate)
		}
	}
	if len(ms.WallSeconds.Components) != 0 {
		t.Errorf("an empty ledger carries no vector total that reads as zero: %+v", ms.WallSeconds)
	}
	if ms.FrontierTokensNum != nil {
		t.Errorf("an empty ledger carries no token total that reads as zero: %v", *ms.FrontierTokensNum)
	}
}

// The table prints a unit's disposition as the row carries it, never a default.
func TestRenderTable_Boundary_DispositionPrintedAsIs(t *testing.T) {
	report := newReport("")
	report.Units = []UnitReport{{PullRequestNumber: 1, HeadBranch: "b"}, {PullRequestNumber: 2, HeadBranch: "c", Disposition: DispositionTimedOut}}
	var out bytes.Buffer
	if err := RenderTable(report, &out); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(out.String(), "\n")
	if strings.Contains(rows[1], DispositionQualified) || !strings.Contains(rows[2], DispositionTimedOut) {
		t.Errorf("rows must carry their own disposition:\n%s", out.String())
	}
}
func TestRenderJSON_Positive_IdentityAndEstimateError(t *testing.T) {
	rep := sampleReport(t)
	errAmt := 0.005
	id := &router.RunIdentity{
		PhysicalModel:  "claude-3-7-sonnet-20250219",
		Harness:        "claude-code",
		HarnessVersion: "1.0.0",
		PromptDigest:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ContextDigest:  "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		ContextBytes:   4096,
		ToolSet:        []string{"bash", "view"},
		PriorRounds:    2,
		Retries:        1,
		CostEstimate:   0.015,
	}
	rep.Units[0].ResolvedModel = id.PhysicalModel
	rep.Units[0].Identity = id
	rep.Units[0].IdentityKey = id.Key()
	rep.Units[0].EstimateError = "$+0.0050"
	rep.Units[0].EstimateErrorAmount = &errAmt
	rep.MilestoneSummary.EstimateError = "$+0.0050"
	rep.MilestoneSummary.EstimateErrorAmount = &errAmt

	var buf bytes.Buffer
	if err := RenderJSON(rep, &buf); err != nil {
		t.Fatalf("RenderJSON failed: %v", err)
	}

	var parsed Report
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.Units[0].Identity == nil || parsed.Units[0].Identity.PhysicalModel != id.PhysicalModel {
		t.Fatalf("expected physical model %s, got %+v", id.PhysicalModel, parsed.Units[0].Identity)
	}
	if parsed.Units[0].IdentityKey != id.Key() {
		t.Errorf("expected identity key %s, got %s", id.Key(), parsed.Units[0].IdentityKey)
	}
	if parsed.Units[0].EstimateError != "$+0.0050" {
		t.Errorf("expected estimate error $+0.0050, got %s", parsed.Units[0].EstimateError)
	}
}

func TestRenderTable_Positive_EstimateError(t *testing.T) {
	rep := sampleReport(t)
	rep.MilestoneSummary.EstimateError = "$+0.0050 (underestimated)"
	var buf bytes.Buffer
	if err := RenderTable(rep, &buf); err != nil {
		t.Fatalf("RenderTable failed: %v", err)
	}
	want := summaryLine("Estimate Error", "$+0.0050 (underestimated)")
	if !strings.Contains(buf.String(), want) {
		t.Errorf("expected output to contain Estimate Error, got:\n%s", buf.String())
	}
}
