// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeFixture(t *testing.T, body string) TranscriptIngestOptions {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return TranscriptIngestOptions{Format: TranscriptFormatClaudeCode, SourcePath: path, CacheDir: filepath.Join(t.TempDir(), "cache")}
}

func claudeMessageFixture(role, content string) string {
	return `{"type":"` + role + `","uuid":"record-id","parentUuid":"parent-id","sessionId":"session-id","timestamp":"2026-09-12T12:00:00.123Z","message":{"role":"` + role + `","content":` + content + `}}` + "\n"
}

const claudeToolContent = `[{"type":"thinking","thinking":"PRIVATE_THINKING","signature":"PRIVATE_SIGNATURE"},{"type":"text","text":"visible"},{"type":"tool_use","id":"tool-id","name":"shell","input":{"command":"NEVER_EXECUTE"}}]`

func TestClaudeIngestionPreservesObservationsAndSkipsMetadata(t *testing.T) {
	body := `{"type":"queue-operation","sessionId":"session-id","content":"PRIVATE_METADATA"}` + "\n" +
		claudeMessageFixture("assistant", claudeToolContent) +
		claudeMessageFixture("user", `[{"type":"tool_result","tool_use_id":"tool-id","is_error":true,"content":[{"type":"text","text":"failure"},{"type":"tool_reference","tool_name":"lookup"}]}]`) +
		claudeMessageFixture("assistant", `[{"type":"thinking","thinking":"PRIVATE_THINKING"}]`)
	opts := claudeFixture(t, body)
	opts.MaxRecords = 2
	first, err := IngestTranscript(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalRecords != 4 || first.Stored != 1 || first.Skipped != 1 || first.ThinkingBlocks != 2 || first.MetadataRecords != 1 || first.Source.Conversation != "session-id" {
		t.Fatalf("first: %+v", first)
	}
	opts.Cursor = first.NextCursor
	last, err := IngestTranscript(context.Background(), opts)
	if err != nil || !last.Complete || last.Stored != 1 || last.Skipped != 1 {
		t.Fatalf("last: %+v %v", last, err)
	}
	opts.Cursor = ""
	opts.MaxRecords = 4
	retry, err := IngestTranscript(context.Background(), opts)
	if err != nil || retry.Stored != 0 || retry.AlreadyPresent != 2 || retry.Skipped != 2 {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	files, err := filepath.Glob(filepath.Join(opts.CacheDir, "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("files: %v %v", files, err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "PRIVATE_") {
			t.Fatal("excluded content persisted")
		}
		var event TranscriptEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.SourceFormat != TranscriptFormatClaudeCode || event.RecordUUID != "record-id" || event.ParentUUID != "parent-id" || event.CreatedAt != "2026-09-12T12:00:00.123Z" {
			t.Fatalf("missing provenance: %+v", event)
		}
		if event.Type == "assistant" && (event.Content != "visible" || len(event.ToolCalls) != 1 || event.ToolCalls[0].ID != "tool-id") {
			t.Fatalf("tool use: %+v", event)
		}
		if event.Type == "user" && (len(event.ToolResults) != 1 || event.ToolResults[0].Content != "failure" || !event.ToolResults[0].IsError || event.ToolResults[0].ToolReferences[0] != "lookup") {
			t.Fatalf("tool result: %+v", event)
		}
	}
	assertTranscriptPrivate(t, opts.CacheDir, files)
}

func TestClaudeRejectsMalformedTailBeforeAnyWrites(t *testing.T) {
	valid := claudeMessageFixture("user", `"prompt"`)
	bad := []string{
		`null`, `[]`, `{"type":"future-unknown"}`, `{"type":"user"}`,
		strings.Replace(valid, `"type":"user"`, `"Type":"user"`, 1),
		strings.Replace(valid, `"uuid":"record-id"`, `"uuid":"one","uuid":"two"`, 1),
		strings.Replace(valid, `"session-id"`, `"other-session"`, 1),
		strings.Replace(valid, `"2026-09-12T12:00:00.123Z"`, `"bad-time"`, 1),
		strings.Replace(valid, `"role":"user"`, `"role":"assistant"`, 1),
		claudeMessageFixture("user", `null`),
		claudeMessageFixture("user", `[{"type":"text","text":null}]`),
		claudeMessageFixture("assistant", `[{"type":"image","source":{}}]`),
		claudeMessageFixture("user", `[{"type":"thinking","thinking":"hidden"}]`),
		claudeMessageFixture("assistant", `[{"type":"tool_use","id":"x","name":"shell","input":null}]`),
		claudeMessageFixture("assistant", `[{"type":"tool_use","id":"x","name":"shell","input":{"x":1,"x":2}}]`),
		claudeMessageFixture("user", `[{"type":"tool_result","tool_use_id":"x","content":null}]`),
		claudeMessageFixture("user", `[{"type":"tool_result","tool_use_id":"x","is_error":null,"content":"x"}]`),
		claudeMessageFixture("user", `[{"type":"tool_result","tool_use_id":"x","content":[{"type":"tool_reference"}]}]`),
		claudeMessageFixture("user", `[{"type":"tool_result","tool_use_id":"x","content":[{"type":"tool_use","id":"x","name":"shell","input":{}}]}]`),
		claudeMessageFixture("assistant", `[{"type":"text","Text":"ambiguous","text":"visible"}]`),
		strings.Replace(valid, "prompt", string([]byte{255}), 1),
	}
	for i, tail := range bad {
		opts := claudeFixture(t, valid+strings.TrimSpace(tail)+"\n")
		opts.MaxRecords = 1
		if _, err := IngestTranscript(context.Background(), opts); err == nil {
			t.Errorf("case %d accepted", i)
		}
		if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
			t.Errorf("case %d wrote cache: %v", i, err)
		}
	}
}

func TestClaudeContentBoundsAndFormatIsolation(t *testing.T) {
	for _, count := range []int{0, 128, 129} {
		content := "[" + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":"x"},`, count), ",") + "]"
		opts := claudeFixture(t, claudeMessageFixture("assistant", content))
		report, err := IngestTranscript(context.Background(), opts)
		if count > 128 {
			if err == nil {
				t.Fatal("block overflow accepted")
			}
			continue
		}
		if err != nil || !report.Complete {
			t.Fatalf("boundary %d: %+v %v", count, report, err)
		}
	}
	opts := claudeFixture(t, claudeMessageFixture("user", `"prompt"`))
	opts.Format = ""
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("default guessed Claude format")
	}
	opts.Format = "unsupported"
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestTranscriptCursorBindsFormatAndRetainsOldAntigravityCursors(t *testing.T) {
	raw, err := encodeTranscriptCursor("sum", TranscriptFormatClaudeCode, "cache", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTranscriptCursor(raw, "sum", TranscriptFormatAntigravity, "cache", 1); err == nil {
		t.Fatal("cross-format cursor accepted")
	}
	old := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"source_sha256":"sum","cache_key":"cache","next_line":1}`))
	if _, err := decodeTranscriptCursor(old, "sum", TranscriptFormatAntigravity, "cache", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTranscriptCursor(old, "sum", TranscriptFormatClaudeCode, "cache", 1); err == nil {
		t.Fatal("old cursor accepted for Claude")
	}
}

func TestClaudeFilenameSelectionDoesNotPreferAntigravitySibling(t *testing.T) {
	opts := claudeFixture(t, claudeMessageFixture("user", `"prompt"`))
	if err := os.WriteFile(filepath.Join(filepath.Dir(opts.SourcePath), "transcript_full.jsonl"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := IngestTranscript(context.Background(), opts)
	if err != nil || report.Source.Path != opts.SourcePath {
		t.Fatalf("selection: %+v %v", report, err)
	}
}
