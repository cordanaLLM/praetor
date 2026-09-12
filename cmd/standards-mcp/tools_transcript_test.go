package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

const mcpTranscriptFixture = `{"step_index":1,"source":"MODEL","type":"MESSAGE","status":"DONE","created_at":"2026-09-12T12:00:00Z","content":"private fixture payload"}` + "\n"

func transcriptMCPReport(t *testing.T, result *mcp.ToolResult) harvester.TranscriptIngestReport {
	t.Helper()
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("ingestion failed: %+v", result)
	}
	var envelope struct {
		Report harvester.TranscriptIngestReport `json:"report"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content[0].Text, "private fixture payload") {
		t.Fatal("MCP report exposed transcript payload")
	}
	return envelope.Report
}

func TestTranscriptMCPPagesAndReplaysActualCache(t *testing.T) {
	srv, root := newFixtureServer(t)
	source := filepath.Join(root, "transcript_full.jsonl")
	if err := os.WriteFile(source, []byte(mcpTranscriptFixture+mcpTranscriptFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"source_path": "transcript_full.jsonl", "cache_dir": "events", "max_records": 1}
	first := transcriptMCPReport(t, callTool(t, srv, "standards_transcript_ingest", args))
	if first.Stored != 1 || first.Complete || first.Remaining != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	args["cursor"] = first.NextCursor
	second := transcriptMCPReport(t, callTool(t, srv, "standards_transcript_ingest", args))
	if second.Stored != 1 || !second.Complete || second.NextCursor != "" {
		t.Fatalf("second page: %+v", second)
	}
	delete(args, "cursor")
	replay := transcriptMCPReport(t, callTool(t, srv, "standards_transcript_ingest", args))
	if replay.Stored != 0 || replay.AlreadyPresent != 1 {
		t.Fatalf("replay duplicated record: %+v", replay)
	}
}

func TestTranscriptMCPRejectsInvalidArgumentsAndOutsidePaths(t *testing.T) {
	srv, _ := newFixtureServer(t)
	for _, args := range []map[string]any{
		{}, {"source_path": true, "cache_dir": "events"},
		{"source_path": t.TempDir(), "cache_dir": "events"},
		{"source_path": "transcript_full.jsonl", "cache_dir": t.TempDir()},
	} {
		if result := callTool(t, srv, "standards_transcript_ingest", args); !result.IsError {
			t.Fatalf("invalid arguments accepted: %+v", args)
		}
	}
	for _, value := range []any{0, -1, 10001, 1.5, "1000", true} {
		if _, err := transcriptBatchArgument(map[string]any{"max_records": value}); err == nil {
			t.Fatalf("invalid batch accepted: %v", value)
		}
	}
}

func TestClaudeTranscriptMCPExplicitFormatAndReplay(t *testing.T) {
	srv, root := newFixtureServer(t)
	source := filepath.Join(root, "session.jsonl")
	body := `{"type":"assistant","uuid":"message-id","sessionId":"session-id","timestamp":"2026-09-12T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"private fixture payload"}]}}` + "\n"
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"source_path": "session.jsonl", "cache_dir": "claude-events"}
	for _, format := range []any{"", true, "unsupported"} {
		args["format"] = format
		if result := callTool(t, srv, "standards_transcript_ingest", args); !result.IsError {
			t.Fatalf("accepted format %v", format)
		}
	}
	args["format"] = harvester.TranscriptFormatClaudeCode
	first := transcriptMCPReport(t, callTool(t, srv, "standards_transcript_ingest", args))
	if first.Source.Format != harvester.TranscriptFormatClaudeCode || first.Stored != 1 || first.ThinkingBlocks != 1 {
		t.Fatalf("report: %+v", first)
	}
	replay := transcriptMCPReport(t, callTool(t, srv, "standards_transcript_ingest", args))
	if replay.Stored != 0 || replay.AlreadyPresent != 1 {
		t.Fatalf("replay: %+v", replay)
	}
}
