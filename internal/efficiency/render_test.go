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

func sampleReport() *Report {
	now := time.Now().Truncate(time.Second).UTC()
	spend1 := 0.15
	touches1 := 2
	tokens1 := int64(500)
	cacheRate1 := 0.75
	localRate1 := 0.25
	itmSecs1 := int64(7200)

	return &Report{
		Units: []UnitReport{
			{
				PullRequestNumber:   101,
				HeadBranch:          "feat/alpha",
				Title:               "Alpha feature",
				Milestone:           "1.0",
				CreatedAt:           now.Add(-2 * time.Hour),
				MergedAt:            now,
				IssueToMerge:        "2h00m",
				IssueToMergeSecs:    &itmSecs1,
				FrontierTokens:      "500",
				FrontierTokensNum:   &tokens1,
				Spend:               "$0.15",
				SpendAmount:         &spend1,
				OperatorTouches:     "2",
				OperatorTouchNum:    &touches1,
				PromptCacheHitRate:  "75.0%",
				CacheHitRatio:       &cacheRate1,
				LocalFirstRatio:     "25.0%",
				LocalRatio:          &localRate1,
				FactHitRatio:        FollowUpRefs,
				ChecksBeforeReviews: FollowUpRefs,
				Sources:             "transcripts+gateway",
				Disposition:         DispositionQualified,
				MetricEpoch:         CurrentMetricEpoch,
				TokensByProvider:    VectorField[map[string]int64]{Value: map[string]int64{"anthropic": 500}, Provenance: ProvenanceMeasured},
				WallSeconds:         VectorField[int64]{Value: 7200, Provenance: ProvenanceMeasured},
				ReviewRounds:        VectorField[int]{Value: 1, Provenance: ProvenanceMeasured},
				Retries:             VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
				OperatorMinutes:     VectorField[float64]{Value: 15.0, Provenance: ProvenanceMeasured},
				EscapedDefects:      VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
			},
		},
		MilestoneSummary: MilestoneSummary{
			Milestone:               "1.0",
			UnitsCount:              1,
			QualifiedUnits:          1,
			LaneCounts:              LaneCounts{Qualified: 1, Offered: 1},
			MetricEpoch:             CurrentMetricEpoch,
			FrontierTokens:          "500",
			FrontierTokensNum:       &tokens1,
			AttributedSpend:         "$0.15",
			AttributedSpendNum:      &spend1,
			UnattributedSpend:       "$0.05",
			TotalSpend:              "$0.20",
			AvgIssueToMerge:         "2h00m",
			AvgIssueToMergeSecs:     &itmSecs1,
			AvgWallSeconds:          "2h00m",
			AvgReviewRounds:         "1.00 per qualified unit",
			AvgRetries:              "0.00 per qualified unit",
			AvgOperatorMinutes:      "15.0m per qualified unit",
			EscapedDefects:          "0 (n=1, rule-of-three bound <= 3.00)",
			EscapedDefectsRate:      "0 (n=1, rule-of-three bound <= 3.00)",
			TokensByProviderDisplay: "anthropic: 500.0/unit",
			OperatorTouches:         "2",
			OperatorTouchNum:        &touches1,
			PromptCacheHitRate:      "75.0%",
			CacheHitRatio:           &cacheRate1,
			LocalFirstRatio:         "25.0%",
			LocalRatio:              &localRate1,
			FactHitRatio:            FollowUpRefs,
			ChecksBeforeReviews:     FollowUpRefs,
		},
		Sources: SourcesMeasured{
			Forge:       true,
			Transcripts: true,
			SpendLog:    true,
		},
	}
}

func TestRenderJSON_Positive_Schema(t *testing.T) {
	rep := sampleReport()
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
	rep := sampleReport()
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

func TestRender_Boundary_EmptyLedgerPrintsUndefinedRates(t *testing.T) {
	rep := newReport("M-Empty")
	var tableBuf bytes.Buffer
	if err := RenderTable(rep, &tableBuf); err != nil {
		t.Fatalf("unexpected error rendering table: %v", err)
	}
	tableOut := tableBuf.String()
	requiredSubstrings := []string{
		"Avg Issue-to-Merge:      undefined",
		"Avg Wall Seconds:        undefined",
		"Avg Review Rounds:       undefined",
		"Avg Retries:             undefined",
		"Avg Operator Minutes:    undefined",
		"Escaped Defects:         undefined",
		"Operator Touches:        undefined",
		"Frontier Tokens:         undefined",
		"Prompt-Cache Hit Rate:   undefined",
		"Local-First Ratio:       undefined",
		"Fact-Hit Ratio:          undefined",
		"Checks-Before-Reviews:   undefined",
		"Lane Counts:             0 qualified, 0 offered",
	}
	for _, sub := range requiredSubstrings {
		if !strings.Contains(tableOut, sub) {
			t.Errorf("empty ledger table output missing %q, got:\n%s", sub, tableOut)
		}
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
	if ms.AvgIssueToMerge != UndefinedRate || ms.AvgWallSeconds != UndefinedRate ||
		ms.AvgReviewRounds != UndefinedRate || ms.AvgRetries != UndefinedRate ||
		ms.AvgOperatorMinutes != UndefinedRate || ms.EscapedDefects != UndefinedRate ||
		ms.FrontierTokens != UndefinedRate || ms.PromptCacheHitRate != UndefinedRate ||
		ms.LocalFirstRatio != UndefinedRate || ms.FactHitRatio != UndefinedRate ||
		ms.ChecksBeforeReviews != UndefinedRate {
		t.Errorf("expected all rates to be undefined in empty ledger JSON, got: %+v", ms)
	}
}
