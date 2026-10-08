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
	// 2. Task-notification user entry on feat/target (origin.kind=task-notification, NOT touch)
	// 3. Peer agent user entry on feat/target (origin.kind=peer, NOT touch)
	// 4. Meta message on feat/target (isMeta=true, NOT touch)
	// 5. Compact summary on feat/target (isCompactSummary=true, NOT touch)
	// 6. Sidechain message on feat/target (isSidechain=true, NOT touch)
	// 7. Assistant response on feat/target with usage (frontier model)
	// 8. Tool result on feat/target (NOT operator touch)
	// 9. Hook attachment on feat/target (NOT operator touch)
	// 10. User entry on unrelated feat/other (must NOT join to feat/target)
	// 11. Assistant response on feat/target with local model (local request = 1)
	transcriptJSONL := `
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"text","text":"Implement feature"}]},"origin":{"kind":"human"}}
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"text","text":"Task updated"}]},"origin":{"kind":"task-notification"}}
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"text","text":"Message from reviewer"}]},"origin":{"kind":"peer"}}
{"type":"user","gitBranch":"feat/target","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Meta injection"}]}}
{"type":"user","gitBranch":"feat/target","isCompactSummary":true,"message":{"role":"user","content":[{"type":"text","text":"Summary"}]}}
{"type":"user","gitBranch":"feat/target","isSidechain":true,"message":{"role":"user","content":[{"type":"text","text":"Sidechain"}]}}
{"type":"assistant","gitBranch":"feat/target","requestId":"req-1","message":{"id":"msg-1","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":150,"output_tokens":40}}}
{"type":"user","gitBranch":"feat/target","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"file content"}]}}
{"type":"attachment","gitBranch":"feat/target","hookName":"SessionStart"}
{"type":"user","gitBranch":"feat/other","message":{"role":"user","content":[{"type":"text","text":"Other branch prompt"}]},"origin":{"kind":"human"}}
{"type":"assistant","gitBranch":"feat/target","requestId":"req-2","message":{"id":"msg-2","model":"ollama/llama3","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":50,"output_tokens":20}}}
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

	// Exactly 1 operator touch (all non-human kinds, isMeta, isCompactSummary, isSidechain excluded)
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

	// Branch feat/other is in byBranch under its own name
	otherStats := byBranch["feat/other"]
	if otherStats == nil || otherStats.OperatorTouches != 1 {
		t.Errorf("expected feat/other to have 1 operator touch under its own branch, got %+v", otherStats)
	}
}

func TestReadTranscriptStream_Positive_DeduplicateMultiBlockResponses(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(&config.EfficiencyPolicy{})

	// 3 lines representing 3 content blocks of the same response:
	// repeating the same requestId, message.id and usage (105 tokens total)
	multiBlockJSONL := `
{"type":"assistant","gitBranch":"feat/dedupe","requestId":"req-multi","message":{"id":"msg-multi","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":55}}}
{"type":"assistant","gitBranch":"feat/dedupe","requestId":"req-multi","message":{"id":"msg-multi","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":55}}}
{"type":"assistant","gitBranch":"feat/dedupe","requestId":"req-multi","message":{"id":"msg-multi","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":55}}}
`

	byBranch := make(map[string]*BranchTranscriptStats)
	err := ReadTranscriptStream(ctx, strings.NewReader(multiBlockJSONL), classifier, byBranch)
	if err != nil {
		t.Fatalf("unexpected error reading multi-block response stream: %v", err)
	}

	stats := byBranch["feat/dedupe"]
	if stats == nil {
		t.Fatal("expected stats for feat/dedupe")
	}

	// Total requests must be 1, not 3!
	if stats.TotalRequests != 1 {
		t.Errorf("expected TotalRequests = 1, got %d", stats.TotalRequests)
	}
	// Frontier tokens must be 105, not 315!
	if stats.FrontierTokens != 105 {
		t.Errorf("expected FrontierTokens = 105, got %d", stats.FrontierTokens)
	}
}

func TestReadTranscriptsDir_Positive_SubagentsDescended(t *testing.T) {
	ctx := context.Background()
	classifier := NewClassifier(nil)
	dir := t.TempDir()

	// Top-level session
	topLevel := filepath.Join(dir, "session-root.jsonl")
	if err := os.WriteFile(topLevel, []byte(`{"type":"user","gitBranch":"main","origin":{"kind":"human"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Subagent subdirectories
	subagentDir := filepath.Join(dir, "session-root", "subagents")
	if err := os.MkdirAll(subagentDir, 0o750); err != nil {
		t.Fatal(err)
	}
	subagent1 := filepath.Join(subagentDir, "agent-1.jsonl")
	if err := os.WriteFile(subagent1, []byte(`{"type":"user","gitBranch":"fix/figures-engine","origin":{"kind":"human"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subagent2 := filepath.Join(subagentDir, "agent-2.jsonl")
	if err := os.WriteFile(subagent2, []byte(`{"type":"user","gitBranch":"fix/docs-strict-build-gate","origin":{"kind":"human"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stats, err := ReadTranscriptsDir(ctx, dir, classifier)
	if err != nil {
		t.Fatalf("unexpected error reading transcripts dir with subagents: %v", err)
	}

	if stats["fix/figures-engine"] == nil || stats["fix/figures-engine"].OperatorTouches != 1 {
		t.Errorf("expected fix/figures-engine subagent to be found with 1 touch, got %+v", stats["fix/figures-engine"])
	}
	if stats["fix/docs-strict-build-gate"] == nil || stats["fix/docs-strict-build-gate"].OperatorTouches != 1 {
		t.Errorf("expected fix/docs-strict-build-gate subagent to be found with 1 touch, got %+v", stats["fix/docs-strict-build-gate"])
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

	dir := t.TempDir()
	filePath := filepath.Join(dir, "session.jsonl")
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(`{"type":"user","gitBranch":"feat/test","message":{"role":"user","content":[{"type":"text","text":"prompt"}]},"origin":{"kind":"human"}}` + "\n")
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
