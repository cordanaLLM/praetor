package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHarvestTranscriptWritesAndReplaysExplicitLocalSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "transcript_full.jsonl")
	body := `{"step_index":1,"source":"MODEL","type":"MESSAGE","status":"DONE","created_at":"2026-09-12T12:00:00Z","content":"fixture"}` + "\n"
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "events")
	args := []string{"transcript", "--source=" + source, "--cache=" + cache}
	for attempt := 0; attempt < 2; attempt++ {
		if err := runHarvest(args); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(cache, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache duplicated or missing: %v / %v", files, err)
	}
}

func TestHarvestTranscriptRequiresExplicitPathsAndBounds(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--source=transcript_full.jsonl"}, {"--cache=events"},
		{"--source=transcript_full.jsonl", "--cache=events", "extra"},
		{"--source=transcript_full.jsonl", "--cache=events", "--max-records=0"},
		{"--source=transcript_full.jsonl", "--cache=events", "--max-records=10001"},
	} {
		if err := runHarvestTranscript(context.Background(), args); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
}
