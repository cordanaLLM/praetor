// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleReport(t *testing.T) *Report {
	t.Helper()
	now := time.Now().Truncate(time.Second).UTC()
	spend1 := 0.15
	touches1 := 2
	tokens1 := int64(500)
	cacheRate1 := 0.75
	localRate1 := 0.25
	itmSecs1 := int64(7200)
	requests, local := 4, 1
	cacheRead, cacheInput := int64(300), int64(400)

	report := &Report{
		Units: []UnitReport{
			{
				PullRequestNumber:    101,
				HeadBranch:           "feat/alpha",
				Title:                "Alpha feature",
				Milestone:            "1.0",
				CreatedAt:            now.Add(-2 * time.Hour),
				MergedAt:             now,
				IssueToMerge:         "2h00m",
				IssueToMergeSecs:     &itmSecs1,
				FrontierTokens:       "500",
				FrontierTokensNum:    &tokens1,
				Spend:                "$0.15",
				SpendAmount:          &spend1,
				OperatorTouches:      "2",
				OperatorTouchNum:     &touches1,
				PromptCacheHitRate:   "75.0%",
				CacheHitRatio:        &cacheRate1,
				CacheReadTokensNum:   &cacheRead,
				PromptInputTokensNum: &cacheInput,
				LocalFirstRatio:      "25.0%",
				LocalRatio:           &localRate1,
				RequestsNum:          &requests,
				LocalRequestsNum:     &local,
				FactHitRatio:         FollowUpRefs,
				ChecksBeforeReviews:  FollowUpRefs,
				Sources:              "transcripts+gateway",
				Disposition:          DispositionQualified,
				MetricEpoch:          CurrentMetricEpoch,
				TokensByProvider:     &VectorField[map[string]int64]{Value: map[string]int64{"anthropic": 500}, Provenance: ProvenanceMeasured},
				WallSeconds:          &VectorField[int64]{Value: 7200, Provenance: ProvenanceMeasured},
				ReviewRounds:         &VectorField[int]{Value: 1, Provenance: ProvenanceMeasured},
				Retries:              &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
				OperatorMinutes:      &VectorField[float64]{Value: 15.0, Provenance: ProvenanceMeasured},
				EscapedDefects:       &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
			},
		},
		MilestoneSummary: MilestoneSummary{Milestone: "1.0"},
		Sources: SourcesMeasured{
			Forge:       true,
			Transcripts: true,
			SpendLog:    true,
		},
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
		"Escaped Defects per Qualified Unit:    0 (n=1, rule-of-three bound <= 3.00) [measured 1]",
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
	if want := summaryLine("Lane Counts", "0 qualified, 0 offered"); !strings.Contains(tableOut, want) {
		t.Errorf("empty ledger lane counts missing %q:\n%s", want, tableOut)
	}

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
	if len(ms.WallSeconds.Components) != 0 || ms.FrontierTokensNum != nil {
		t.Errorf("an empty ledger carries no numbers that read as zero: %+v", ms)
	}
}
