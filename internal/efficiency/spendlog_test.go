// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestReadSpendLogJSONL_Positive_AttributionAndUnattributed(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(&config.EfficiencyPolicy{})

	branchToPR := map[string]int{
		"feat/landing": 101,
		"feat/other":   102,
	}
	validPRs := map[int]bool{101: true, 102: true}

	// 1. Entry with tags naming branch:feat/landing -> attributed to PR 101
	// 2. Entry with metadata naming PR 102 -> attributed to PR 102
	// 3. Entry without tags/metadata -> counted as UNATTRIBUTED (never spread!)
	// 4. Entry with unknown branch -> counted as UNATTRIBUTED
	// 5. Entry with spend 0.0 for local model -> NOT dropped! Counted for PR 101 local requests
	jsonl := `
{"model":"claude-3-7-sonnet","spend":0.05,"tokens":500,"tags":["branch:feat/landing"]}
{"model":"gpt-4o","cost":0.10,"tokens":800,"metadata":{"pr":102}}
{"model":"claude-3-5-sonnet","spend":0.25}
{"model":"o3-mini","spend":0.15,"tags":["branch:feat/random-unrelated"]}
{"model":"ollama/llama3","spend":0.0,"tokens":200,"tags":["branch:feat/landing"]}
`

	report := &SpendReport{
		SpendByPRNumber:          make(map[int]float64),
		RequestsByPRNumber:       make(map[int]int),
		LocalRequestsByPRNumber:  make(map[int]int),
		FrontierTokensByPRNumber: make(map[int]int64),
	}
	err := ReadSpendLogJSONL(ctx, strings.NewReader(jsonl), classifier, branchToPR, validPRs, report)
	if err != nil {
		t.Fatalf("unexpected error reading spend log JSONL: %v", err)
	}

	if report.SpendByPRNumber[101] != 0.05 {
		t.Errorf("expected PR 101 spend 0.05, got %v", report.SpendByPRNumber[101])
	}
	if report.SpendByPRNumber[102] != 0.10 {
		t.Errorf("expected PR 102 spend 0.10, got %v", report.SpendByPRNumber[102])
	}
	// Total unattributed = 0.25 + 0.15 = 0.40
	if report.Unattributed < 0.39 || report.Unattributed > 0.41 {
		t.Errorf("expected unattributed spend ~0.40, got %v", report.Unattributed)
	}

	// PR 101 had 2 requests: 1 claude-3-7-sonnet (frontier, 500 tokens), 1 ollama/llama3 (local, spend=0)
	if report.RequestsByPRNumber[101] != 2 {
		t.Errorf("expected PR 101 requests = 2, got %d", report.RequestsByPRNumber[101])
	}
	if report.LocalRequestsByPRNumber[101] != 1 {
		t.Errorf("expected PR 101 local requests = 1, got %d", report.LocalRequestsByPRNumber[101])
	}
	if report.FrontierTokensByPRNumber[101] != 500 {
		t.Errorf("expected PR 101 frontier tokens = 500, got %d", report.FrontierTokensByPRNumber[101])
	}
}

func TestReadSpendLogCSV_Positive_Attribution(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(&config.EfficiencyPolicy{})

	branchToPR := map[string]int{"feat/landing": 101}
	validPRs := map[int]bool{101: true}

	csvData := `model,spend,branch,tags,tokens
claude-3-7-sonnet,0.05,feat/landing,,500
gpt-4o,0.12,,pr:101,600
claude-3-5-sonnet,0.30,,,
ollama/llama3,0.00,feat/landing,,300
`

	report := &SpendReport{
		SpendByPRNumber:          make(map[int]float64),
		RequestsByPRNumber:       make(map[int]int),
		LocalRequestsByPRNumber:  make(map[int]int),
		FrontierTokensByPRNumber: make(map[int]int64),
	}
	err := ReadSpendLogCSV(ctx, strings.NewReader(csvData), classifier, branchToPR, validPRs, report)
	if err != nil {
		t.Fatalf("unexpected error reading spend log CSV: %v", err)
	}

	// PR 101 has 0.05 + 0.12 = 0.17
	if report.SpendByPRNumber[101] < 0.16 || report.SpendByPRNumber[101] > 0.18 {
		t.Errorf("expected PR 101 spend ~0.17, got %v", report.SpendByPRNumber[101])
	}
	if report.Unattributed < 0.29 || report.Unattributed > 0.31 {
		t.Errorf("expected unattributed spend ~0.30, got %v", report.Unattributed)
	}
	if report.LocalRequestsByPRNumber[101] != 1 {
		t.Errorf("expected PR 101 local requests = 1, got %d", report.LocalRequestsByPRNumber[101])
	}
}

func TestReadSpendLogFile_Positive_FileDetection(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(&config.EfficiencyPolicy{})
	dir := t.TempDir()

	prs := []forge.MergedPullRequest{{Number: 42, HeadBranch: "feat/answer"}}

	// Test with JSONL file
	jsonlPath := filepath.Join(dir, "spend.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(`{"model":"claude-3-7-sonnet","spend":0.42,"tokens":100,"tags":["branch:feat/answer"]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sr, err := ReadSpendLogFile(ctx, jsonlPath, prs, classifier)
	if err != nil {
		t.Fatalf("unexpected error reading JSONL file: %v", err)
	}
	if sr.SpendByPRNumber[42] != 0.42 {
		t.Errorf("expected PR 42 spend 0.42, got %v", sr.SpendByPRNumber[42])
	}

	// Test with CSV file
	csvPath := filepath.Join(dir, "spend.csv")
	if err := os.WriteFile(csvPath, []byte("model,spend,branch\nclaude-3-7-sonnet,0.84,feat/answer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srCSV, err := ReadSpendLogFile(ctx, csvPath, prs, classifier)
	if err != nil {
		t.Fatalf("unexpected error reading CSV file: %v", err)
	}
	if srCSV.SpendByPRNumber[42] != 0.84 {
		t.Errorf("expected PR 42 spend 0.84, got %v", srCSV.SpendByPRNumber[42])
	}
}

func TestReadSpendLogFile_Negative_MissingAndEmpty(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(nil)

	// Empty path
	if _, err := ReadSpendLogFile(ctx, "", nil, classifier); err == nil {
		t.Error("expected error for empty spend log path")
	}

	// Non-existent path
	if _, err := ReadSpendLogFile(ctx, "/nonexistent/spend.jsonl", nil, classifier); err == nil {
		t.Error("expected error for nonexistent spend log file")
	}

	// Malformed JSONL
	dir := t.TempDir()
	badJSONL := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(badJSONL, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSpendLogFile(ctx, badJSONL, nil, classifier); err == nil {
		t.Error("expected error for malformed JSONL spend log file")
	}
}

func TestReadSpendLogFile_Boundary_ContextCancellation(t *testing.T) {
	cancellingCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ReadSpendLogFile(cancellingCtx, t.TempDir(), nil, nil); err == nil {
		t.Error("expected error with cancelled context")
	}
}
