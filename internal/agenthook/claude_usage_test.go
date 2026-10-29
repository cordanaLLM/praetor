package agenthook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/router"
)

const plantedClaudeUsage = `{"type":"assistant","message":{"usage":{"input_tokens":11,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}}
{"type":"user","message":{"usage":{"input_tokens":999}}}
{"type":"assistant","message":{"usage":{"input_tokens":13,"output_tokens":17,"cache_creation_input_tokens":19,"cache_read_input_tokens":23}}}
`

func plantClaudeTranscript(t *testing.T, data string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session", "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-agent.jsonl", "agent-other.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "agent-agent.jsonl")
}

func TestReadClaudeUsage(t *testing.T) {
	transcript := plantClaudeTranscript(t, plantedClaudeUsage)
	got, err := readClaudeUsage(t.Context(), transcript, "agent")
	if err != nil || got != 98 {
		t.Fatalf("whole-run tokens=%d want 98: %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "not-subagents", "agent-agent.jsonl")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(plantedClaudeUsage), 0o600); err != nil {
		t.Fatal(err)
	}
	otherAgent := filepath.Join(filepath.Dir(transcript), "agent-other.jsonl")
	for _, tc := range []struct{ path, agent string }{
		{otherAgent, "agent"},
		{outside, "agent"},
		{filepath.Join("subagents", "agent-agent.jsonl"), "agent"},
		{filepath.Join(filepath.Dir(transcript), "agent-missing.jsonl"), "missing"},
		{transcript, "../agent"},
		{"", "agent"},
	} {
		if tokens, err := readClaudeUsage(t.Context(), tc.path, tc.agent); err == nil || tokens != 0 {
			t.Fatalf("invalid path (%q, %q) read: tokens=%d err=%v", tc.path, tc.agent, tokens, err)
		}
	}
}

func TestClaudeUsageBounds(t *testing.T) {
	for _, data := range []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":-1}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":1000000000001}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":1000000000000,"output_tokens":1}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":9223372036854775807}}}`,
		`{"type":"assistant","type":"user"}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":"1"}}}`,
		strings.Repeat("x", maxClaudeTranscriptLineBytes+1),
		"{broken",
		strings.Repeat("{}\n", maxClaudeTranscriptLines+1),
	} {
		if tokens, err := sumClaudeUsage(t.Context(), []byte(data)); err == nil || tokens != 0 {
			t.Fatalf("invalid usage accepted: tokens=%d err=%v", tokens, err)
		}
	}
	data := []byte(`{"type":"assistant","message":{"usage":{"input_tokens":1000000000000}}}`)
	if tokens, err := sumClaudeUsage(t.Context(), data); err != nil || tokens != router.MaxOutcomeTokens {
		t.Fatalf("exact cap: %d %v", tokens, err)
	}
}

func TestClaudeUsageIgnoresUnrelatedUsageMetadata(t *testing.T) {
	data := []byte(`{"type":"assistant","message":{"usage":{"input_tokens":1,"service_tier":"standard","cache_creation":{"ephemeral_5m_input_tokens":1}}}}`)
	if tokens, err := sumClaudeUsage(t.Context(), data); err != nil || tokens != 1 {
		t.Fatalf("usage metadata: %d %v", tokens, err)
	}
}

func TestClaudeUsageOversize(t *testing.T) {
	path := plantClaudeTranscript(t, "")
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	resizeErr := file.Truncate(maxClaudeTranscriptBytes + 1)
	closeErr := file.Close()
	if resizeErr != nil || closeErr != nil {
		t.Fatalf("plant oversize: %v %v", resizeErr, closeErr)
	}
	if tokens, err := readClaudeUsage(t.Context(), path, "agent"); err == nil || tokens != 0 {
		t.Fatalf("oversize accepted: %d %v", tokens, err)
	}
}
