// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func transcriptFixture(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "conversation-1", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Matches the observed original schema; payloads here are deliberately synthetic.
const transcriptEvent = `{"step_index":7,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-11T12:00:00Z","content":"fixture tool result","thinking":"private reasoning must not be persisted","tool_calls":[{"name":"run_command","args":{"command":"printf fixture"}}],"exit_code":0}` + "\n"
const transcriptMetadata = `{"step_index":8,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-09-11T12:00:01Z"}` + "\n"

func TestIngestTranscriptPreservesEventsAndResumes(t *testing.T) {
	path := transcriptFixture(t, transcriptEvent+transcriptMetadata+transcriptEvent)
	cache := filepath.Join(t.TempDir(), "cache")
	options := TranscriptIngestOptions{SourcePath: path, CacheDir: cache, MaxRecords: 2}
	first, err := IngestTranscript(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Stored != 1 || first.Skipped != 1 || first.Remaining != 1 || first.NextCursor == "" {
		t.Fatalf("first report: %+v", first)
	}
	options.Cursor = first.NextCursor
	second, err := IngestTranscript(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete || second.Stored != 1 || second.Remaining != 0 || second.NextCursor != "" {
		t.Fatalf("second report: %+v", second)
	}
	options.Cursor = ""
	replay, err := IngestTranscript(context.Background(), options)
	if err != nil || replay.Stored != 0 || replay.AlreadyPresent != 1 {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	files, err := filepath.Glob(filepath.Join(cache, "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("cache files: %v, %v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private reasoning") || !strings.Contains(string(raw), "fixture tool result") {
		t.Fatalf("bad cache content")
	}
	var event TranscriptEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.SourceSHA256 != first.Source.SHA256 || event.StepIndex != 7 || len(event.ToolCalls) != 1 || event.CreatedAt != "2026-09-11T12:00:00Z" {
		t.Fatalf("event provenance missing: %+v", event)
	}
	assertTranscriptPrivate(t, cache, files)
}

func assertTranscriptPrivate(t *testing.T, directory string, files []string) {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if util.ModeIsProtection() && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("public cache directory mode %o", info.Mode().Perm())
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if util.ModeIsProtection() && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("public cache record mode %o", info.Mode().Perm())
		}
	}
}

func TestIngestTranscriptPrefersFullAndRejectsChangedCursor(t *testing.T) {
	full := transcriptFixture(t, transcriptEvent+transcriptEvent)
	short := filepath.Join(filepath.Dir(full), "transcript.jsonl")
	if err := os.WriteFile(short, []byte("invalid shorter export"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := TranscriptIngestOptions{SourcePath: short, CacheDir: t.TempDir(), MaxRecords: 1}
	first, err := IngestTranscript(context.Background(), options)
	if err != nil || first.Source.Path != full {
		t.Fatalf("full selection: %+v, %v", first, err)
	}
	options.SourcePath = full
	replay, err := IngestTranscript(context.Background(), options)
	if err != nil || replay.AlreadyPresent != 1 {
		t.Fatalf("alias replay: %+v, %v", replay, err)
	}
	if err := os.WriteFile(full, []byte(transcriptEvent), 0o600); err != nil {
		t.Fatal(err)
	}
	options.Cursor = first.NextCursor
	if _, err := IngestTranscript(context.Background(), options); err == nil {
		t.Fatal("changed source cursor accepted")
	}
}

func TestIngestTranscriptRejectsMalformedTailBeforeWriting(t *testing.T) {
	for _, tail := range []string{"{bad}\n", `{"type":"user","message":{}}` + "\n", strings.ReplaceAll(transcriptEvent, "fixture tool result", string([]byte{255})), strings.ReplaceAll(transcriptEvent, "\"step_index\":7", "\"step_index\":-1")} {
		t.Run("invalid", func(t *testing.T) {
			source := transcriptFixture(t, transcriptEvent+tail)
			cache := filepath.Join(t.TempDir(), "untouched")
			_, err := IngestTranscript(context.Background(), TranscriptIngestOptions{SourcePath: source, CacheDir: cache, MaxRecords: 1})
			if err == nil {
				t.Fatal("malformed source accepted")
			}
			if _, err := os.Stat(cache); !os.IsNotExist(err) {
				t.Fatalf("validation mutated destination: %v", err)
			}
		})
	}
}

func TestIngestTranscriptEmptyAndInvalidOptions(t *testing.T) {
	source := transcriptFixture(t, "")
	options := TranscriptIngestOptions{SourcePath: source, CacheDir: filepath.Join(t.TempDir(), "cache")}
	report, err := IngestTranscript(context.Background(), options)
	if err != nil || !report.Complete || report.TotalRecords != 0 || report.Stored != 0 {
		t.Fatalf("empty: %+v, %v", report, err)
	}
	for _, limit := range []int{-1, MaxTranscriptBatchRecords + 1} {
		options.MaxRecords = limit
		if _, err := IngestTranscript(context.Background(), options); err == nil {
			t.Fatalf("limit %d accepted", limit)
		}
	}
	options.MaxRecords = 1
	options.Cursor = "forged cursor"
	if _, err := IngestTranscript(context.Background(), options); err == nil {
		t.Fatal("bad cursor accepted")
	}
	if _, err := IngestTranscript(nil, options); err == nil { //nolint:staticcheck // nil-context rejection is the public boundary under test
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := IngestTranscript(ctx, options); err == nil {
		t.Fatal("cancellation ignored")
	}
}
