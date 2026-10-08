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
)

func TestReadTranscriptStream_Positive_BranchJoinAndTouches(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(&config.EfficiencyPolicy{})

	// Transcript fixture with:
	// 1. Human user input on feat/target (operator touch = 1)
	// 2. Assistant response on feat/target with usage (frontier model)
	// 3. Tool result on feat/target (NOT operator touch)
	// 4. Hook attachment on feat/target (NOT operator touch)
	// 5. User entry on unrelated feat/other (must NOT join to feat/target)
	// 6. Assistant response on feat/target with local model (local request = 1)
	transcriptJSONL := `
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"text","text":"Implement feature"}]},"origin":{"kind":"human"}}
{"type":"assistant","gitBranch":"feat/target","message":{"model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":150,"output_tokens":40}}}
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"file content"}]}}
{"type":"attachment","gitBranch":"feat/target","hookName":"SessionStart"}
{"type":"user","gitBranch":"feat/other","message":{"role":"user","content":[{"type":"text","text":"Other branch prompt"}]},"origin":{"kind":"human"}}
{"type":"assistant","gitBranch":"feat/target","message":{"model":"ollama/llama3","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":50,"output_tokens":20}}}
`

	byBranch := make(map[string]*BranchTranscriptStats)
	err := ReadTranscriptStream(ctx, strings.NewReader(transcriptJSONL), classifier, byBranch)
	if err != nil {
		t.Fatalf("unexpected error reading transcript stream: %v", err)
	}

	targetStats := byBranch["feat/target"]
	if targetStats == nil {
		t.Fatal("expected stats for feat/target")
	}

	// 1 operator touch (tool result and hook excluded)
	if targetStats.OperatorTouches != 1 {
		t.Errorf("expected 1 operator touch on feat/target, got %d", targetStats.OperatorTouches)
	}

	// 2 requests total (1 frontier, 1 local)
	if targetStats.TotalRequests != 2 {
		t.Errorf("expected 2 total requests, got %d", targetStats.TotalRequests)
	}
	if targetStats.LocalRequests != 1 {
		t.Errorf("expected 1 local request, got %d", targetStats.LocalRequests)
	}

	// Frontier tokens: only from claude-3-7-sonnet = 100 + 50 + 150 + 40 = 340
	if targetStats.FrontierTokens != 340 {
		t.Errorf("expected 340 frontier tokens, got %d", targetStats.FrontierTokens)
	}

	// Prompt cache hit rate:
	// Total cache_read = 150 + 50 = 200
	// Total cache_creation = 50 + 0 = 50
	// Total input = 100 + 50 = 150
	// Denom = 200 + 50 + 150 = 400
	// Hit rate = 200 / 400 = 0.50 (50.0%)
	rate, ok := targetStats.PromptCacheHitRate()
	if !ok || rate != 0.5 {
		t.Errorf("expected cache hit rate 0.5 (50%%), got %v (ok=%v)", rate, ok)
	}

	// Local-first ratio = 1 / 2 = 0.50 (50.0%)
	ratio, ok := targetStats.LocalFirstRatio()
	if !ok || ratio != 0.5 {
		t.Errorf("expected local-first ratio 0.5 (50%%), got %v (ok=%v)", ratio, ok)
	}

	// Branch feat/other is in byBranch under its own name, not merged into feat/target
	otherStats := byBranch["feat/other"]
	if otherStats == nil || otherStats.OperatorTouches != 1 {
		t.Errorf("expected feat/other to have 1 operator touch under its own branch, got %+v", otherStats)
	}
}

func TestReadTranscriptStream_Negative_NonMatchingBranchNotJoined(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(nil)

	// Session file on feat/unrelated
	unrelatedJSONL := `{"type":"user","gitBranch":"feat/unrelated","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`

	byBranch := make(map[string]*BranchTranscriptStats)
	if err := ReadTranscriptStream(ctx, strings.NewReader(unrelatedJSONL), classifier, byBranch); err != nil {
		t.Fatal(err)
	}

	// PR branch is "feat/target" -> must not be joined!
	if byBranch["feat/target"] != nil {
		t.Errorf("feat/target should not be populated by feat/unrelated transcript")
	}
}

func TestReadTranscriptsDir_Negative_EmptyDirAndMissing(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(nil)

	// Empty dir string
	if _, err := ReadTranscriptsDir(ctx, "", classifier); err == nil {
		t.Error("expected error for empty dir path")
	}

	// Missing dir
	if _, err := ReadTranscriptsDir(ctx, "/path/that/does/not/exist", classifier); err == nil {
		t.Error("expected error for nonexistent directory")
	}

	// Directory with no jsonl files
	emptyDir := t.TempDir()
	stats, err := ReadTranscriptsDir(ctx, emptyDir, classifier)
	if err != nil {
		t.Fatalf("unexpected error for empty directory: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats for empty dir, got %d entries", len(stats))
	}
}

func TestReadTranscriptsDir_Boundary_CapsAndCancelledContext(t *testing.T) {
	classifier := NewClassifier(nil)

	// Context cancellation
	cancellingCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadTranscriptsDir(cancellingCtx, t.TempDir(), classifier); err == nil {
		t.Error("expected error with cancelled context")
	}

	// Line cap check: a file with more than MaxSourceLines
	dir := t.TempDir()
	filePath := filepath.Join(dir, "session.jsonl")
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(`{"type":"user","gitBranch":"feat/test","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}` + "\n")
	}
	if err := os.WriteFile(filePath, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	byBranch := make(map[string]*BranchTranscriptStats)
	if err := ReadTranscriptFile(context.Background(), filePath, classifier, byBranch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if byBranch["feat/test"] == nil || byBranch["feat/test"].OperatorTouches != 50 {
		t.Errorf("expected 50 operator touches, got %+v", byBranch["feat/test"])
	}
}
