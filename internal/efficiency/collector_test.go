// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestCollector_Positive_MilestoneSummaryOverTwoUnits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t0 := time.Now().Add(-5 * time.Hour).Truncate(time.Second).UTC()
	t1 := t0.Add(2 * time.Hour) // Issue 1 created
	t2 := t1.Add(1 * time.Hour) // PR 1 created
	t3 := t2.Add(1 * time.Hour) // PR 1 merged (issue-to-merge = 2h)

	t4 := t0.Add(1 * time.Hour) // PR 2 created
	t5 := t4.Add(3 * time.Hour) // PR 2 merged (issue-to-merge = 3h)

	// Forge JSON fixture: 2 PRs in milestone "1.0"
	forgeJSON := fmt.Sprintf(`[
		{
			"number": 101,
			"head_branch": "feat/pr-one",
			"title": "Slice 1 Feature",
			"milestone": "1.0",
			"created_at": %q,
			"merged_at": %q,
			"closing_issues": [
				{
					"number": 10,
					"created_at": %q
				}
			]
		},
		{
			"number": 102,
			"head_branch": "feat/pr-two",
			"title": "Slice 2 Feature",
			"milestone": "1.0",
			"created_at": %q,
			"merged_at": %q
		}
	]`, t2.Format(time.RFC3339), t3.Format(time.RFC3339), t1.Format(time.RFC3339),
		t4.Format(time.RFC3339), t5.Format(time.RFC3339))

	forgePath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(forgePath, []byte(forgeJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Transcripts directory fixture
	transDir := filepath.Join(dir, "transcripts")
	if err := os.MkdirAll(transDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// PR 1: 1 operator touch, 100 frontier tokens, 50 cache read, 50 input
	// PR 2: 2 operator touches, 200 frontier tokens, 100 cache read, 100 input
	pr1Transcript := `
{"type":"user","gitBranch":"feat/pr-one","message":{"role":"user","content":[{"type":"text","text":"start"}]},"origin":{"kind":"human"}}
{"type":"assistant","gitBranch":"feat/pr-one","message":{"model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":50,"output_tokens":0}}}
`
	pr2Transcript := `
{"type":"user","gitBranch":"feat/pr-two","message":{"role":"user","content":[{"type":"text","text":"part 1"}]},"origin":{"kind":"human"}}
{"type":"user","gitBranch":"feat/pr-two","message":{"role":"user","content":[{"type":"text","text":"part 2"}]},"origin":{"kind":"human"}}
{"type":"assistant","gitBranch":"feat/pr-two","message":{"model":"claude-3-5-sonnet","role":"assistant","usage":{"input_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":0}}}
`
	if err := os.WriteFile(filepath.Join(transDir, "pr1.jsonl"), []byte(pr1Transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transDir, "pr2.jsonl"), []byte(pr2Transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	// Spend log fixture: PR 1 = $0.10, PR 2 = $0.20, Unattributed = $0.05
	spendJSONL := `
{"model":"claude-3-7-sonnet","spend":0.10,"tags":["branch:feat/pr-one"]}
{"model":"claude-3-5-sonnet","spend":0.20,"tags":["branch:feat/pr-two"]}
{"model":"o1","spend":0.05}
`
	spendPath := filepath.Join(dir, "spend.jsonl")
	if err := os.WriteFile(spendPath, []byte(spendJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := CollectorOptions{
		Root:           dir,
		Milestone:      "1.0",
		ForgeJSONPath:  forgePath,
		TranscriptsDir: transDir,
		SpendLogPath:   spendPath,
	}

	collector := NewCollector(opts)
	report, err := collector.Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error collecting report: %v", err)
	}

	// 1. Two units
	if len(report.Units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(report.Units))
	}

	// Unit 1 assertions
	u1 := report.Units[0]
	if u1.PullRequestNumber != 101 || u1.HeadBranch != "feat/pr-one" {
		t.Errorf("unexpected unit 1 head: %+v", u1)
	}
	if u1.OperatorTouches != "1" {
		t.Errorf("unit 1 operator touches = %s, want 1", u1.OperatorTouches)
	}
	if u1.Spend != "$0.10" {
		t.Errorf("unit 1 spend = %s, want $0.10", u1.Spend)
	}
	if u1.FrontierTokens != "100" {
		t.Errorf("unit 1 frontier tokens = %s, want 100", u1.FrontierTokens)
	}
	if u1.PromptCacheHitRate != "50.0%" {
		t.Errorf("unit 1 prompt cache hit rate = %s, want 50.0%%", u1.PromptCacheHitRate)
	}
	if u1.IssueToMerge != "2h00m" {
		t.Errorf("unit 1 issue to merge = %s, want 2h00m", u1.IssueToMerge)
	}
	if u1.FactHitRatio != FollowUpRefs || u1.ChecksBeforeReviews != FollowUpRefs {
		t.Errorf("unmeasured ratios must reference follow-up issue #881")
	}

	// Unit 2 assertions
	u2 := report.Units[1]
	if u2.PullRequestNumber != 102 || u2.HeadBranch != "feat/pr-two" {
		t.Errorf("unexpected unit 2 head: %+v", u2)
	}
	if u2.OperatorTouches != "2" {
		t.Errorf("unit 2 operator touches = %s, want 2", u2.OperatorTouches)
	}
	if u2.Spend != "$0.20" {
		t.Errorf("unit 2 spend = %s, want $0.20", u2.Spend)
	}
	if u2.FrontierTokens != "200" {
		t.Errorf("unit 2 frontier tokens = %s, want 200", u2.FrontierTokens)
	}
	if u2.IssueToMerge != "3h00m" {
		t.Errorf("unit 2 issue to merge = %s, want 3h00m", u2.IssueToMerge)
	}

	// 2. Milestone Summary over two units
	summary := report.MilestoneSummary
	if summary.UnitsCount != 2 {
		t.Errorf("summary units count = %d, want 2", summary.UnitsCount)
	}
	if summary.Milestone != "1.0" {
		t.Errorf("summary milestone = %s, want 1.0", summary.Milestone)
	}
	// Total touches = 1 + 2 = 3
	if summary.OperatorTouches != "3" {
		t.Errorf("summary operator touches = %s, want 3", summary.OperatorTouches)
	}
	// Total frontier tokens = 100 + 200 = 300
	if summary.FrontierTokens != "300" {
		t.Errorf("summary frontier tokens = %s, want 300", summary.FrontierTokens)
	}
	// Attributed spend = 0.10 + 0.20 = $0.30
	if summary.AttributedSpend != "$0.30" {
		t.Errorf("summary attributed spend = %s, want $0.30", summary.AttributedSpend)
	}
	// Unattributed spend = $0.05
	if summary.UnattributedSpend != "$0.05" {
		t.Errorf("summary unattributed spend = %s, want $0.05", summary.UnattributedSpend)
	}
	// Total spend = $0.35
	if summary.TotalSpend != "$0.35" {
		t.Errorf("summary total spend = %s, want $0.35", summary.TotalSpend)
	}
	// Average issue-to-merge = (2h + 3h) / 2 = 2h30m
	if summary.AvgIssueToMerge != "2h30m" {
		t.Errorf("summary avg issue to merge = %s, want 2h30m", summary.AvgIssueToMerge)
	}
	// Overall prompt cache hit rate = (50 + 100) / (100 + 200) = 150 / 300 = 50.0%
	if summary.PromptCacheHitRate != "50.0%" {
		t.Errorf("summary prompt cache hit rate = %s, want 50.0%%", summary.PromptCacheHitRate)
	}
	// Fact-hit and checks-before-reviews follow up pointers
	if summary.FactHitRatio != FollowUpRefs || summary.ChecksBeforeReviews != FollowUpRefs {
		t.Errorf("milestone summary follow-up pointers incorrect")
	}
}

func TestCollector_Negative_MissingSourcesPrintNotMeasuredNeverZero(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Only forge records provided; Transcripts and SpendLog are absent!
	forgeJSON := `[{"number": 201, "head_branch": "feat/solo", "title": "Solo", "created_at": "2026-10-01T10:00:00Z", "merged_at": "2026-10-01T11:00:00Z"}]`
	forgePath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(forgePath, []byte(forgeJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := CollectorOptions{
		Root:          dir,
		ForgeJSONPath: forgePath,
		// TranscriptsDir and SpendLogPath deliberately empty!
	}

	collector := NewCollector(opts)
	report, err := collector.Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(report.Units))
	}

	u := report.Units[0]
	// Missing transcripts MUST be "not measured", never "0"
	if u.FrontierTokens != NotMeasured {
		t.Errorf("expected FrontierTokens to be %q, got %q", NotMeasured, u.FrontierTokens)
	}
	if u.OperatorTouches != NotMeasured {
		t.Errorf("expected OperatorTouches to be %q, got %q", NotMeasured, u.OperatorTouches)
	}
	if u.PromptCacheHitRate != NotMeasured {
		t.Errorf("expected PromptCacheHitRate to be %q, got %q", NotMeasured, u.PromptCacheHitRate)
	}
	if u.LocalFirstRatio != NotMeasured {
		t.Errorf("expected LocalFirstRatio to be %q, got %q", NotMeasured, u.LocalFirstRatio)
	}

	// Missing spend log MUST be "not measured", never "$0.00"
	if u.Spend != NotMeasured {
		t.Errorf("expected Spend to be %q, got %q", NotMeasured, u.Spend)
	}

	// Milestone summary missing fields must also be "not measured"
	ms := report.MilestoneSummary
	if ms.FrontierTokens != NotMeasured || ms.OperatorTouches != NotMeasured ||
		ms.PromptCacheHitRate != NotMeasured || ms.LocalFirstRatio != NotMeasured ||
		ms.AttributedSpend != NotMeasured || ms.UnattributedSpend != NotMeasured ||
		ms.TotalSpend != NotMeasured {
		t.Errorf("milestone summary must report %q for missing sources, got %+v", NotMeasured, ms)
	}
}

func TestCollector_Boundary_EmptySourcesAndLimits(t *testing.T) {
	ctx := context.Background()

	// Empty collector (no sources)
	collector := NewCollector(CollectorOptions{})
	report, err := collector.Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error on empty collector: %v", err)
	}
	if len(report.Units) != 0 {
		t.Errorf("expected 0 units on empty collector, got %d", len(report.Units))
	}
	if report.MilestoneSummary.UnitsCount != 0 {
		t.Errorf("expected 0 units count in summary, got %d", report.MilestoneSummary.UnitsCount)
	}

	// Limit test with forge PRs
	fakePRs := []forge.MergedPullRequest{
		{Number: 1, HeadBranch: "b1"},
		{Number: 2, HeadBranch: "b2"},
		{Number: 3, HeadBranch: "b3"},
	}
	collectorWithLimit := NewCollector(CollectorOptions{Limit: 2})
	filtered := collectorWithLimit.filterPRs(fakePRs)
	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered PRs with Limit=2, got %d", len(filtered))
	}
}
