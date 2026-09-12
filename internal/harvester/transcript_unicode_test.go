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
)

func TestTranscriptRejectsUnpairedSurrogatesBeforeCacheWrites(t *testing.T) {
	samples := []string{`"\ud800"`, `"\udbff"`, `"\udc00"`, `"\udfff"`, `"\ud800x"`, `"\ud800\u0041"`, `"\ud800\\udc00"`, `"\ud800\ud800"`, `"\udc00\ud800"`}
	for _, content := range samples {
		claude := claudeFixture(t, claudeMessageFixture("user", content))
		antigravity := TranscriptIngestOptions{SourcePath: transcriptFixture(t, strings.Replace(transcriptEvent, `"fixture tool result"`, content, 1)), CacheDir: filepath.Join(t.TempDir(), "cache")}
		for _, opts := range []TranscriptIngestOptions{claude, antigravity} {
			if _, err := IngestTranscript(context.Background(), opts); err == nil {
				t.Errorf("%s accepted %s", opts.Format, content)
			}
			if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
				t.Errorf("%s wrote invalid observation", opts.Format)
			}
		}
	}
}

func TestTranscriptSurrogateValidationCoversNestedToolArgumentsAndKeys(t *testing.T) {
	for _, args := range []string{`{"nested":["\ud800"]}`, `{"\udfff":"value"}`, `{"nested":{"\ud800":"value"}}`} {
		claude := claudeFixture(t, claudeMessageFixture("assistant", `[{"type":"tool_use","id":"id","name":"tool","input":`+args+`}]`))
		antigravity := TranscriptIngestOptions{SourcePath: transcriptFixture(t, strings.Replace(transcriptEvent, `{"command":"printf fixture"}`, args, 1)), CacheDir: filepath.Join(t.TempDir(), "cache")}
		for _, opts := range []TranscriptIngestOptions{claude, antigravity} {
			if _, err := IngestTranscript(context.Background(), opts); err == nil {
				t.Errorf("%s accepted nested invalid code unit", opts.Format)
			}
			if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
				t.Errorf("%s mutated cache", opts.Format)
			}
		}
	}
}

func TestTranscriptPreservesValidUnicodeAndLiteralEscapes(t *testing.T) {
	samples := map[string]string{`"\ud800\udc00"`: "𐀀", `"\uDBFF\uDFFF"`: "\U0010ffff", `"\ud83d\ude42"`: "🙂", `"\ud7ff\ue000"`: "\ud7ff\ue000", `"\\ud800"`: `\ud800`, `"\\\"\ud83d\ude42"`: "\\\"🙂", `"�"`: "�"}
	for raw, want := range samples {
		opts := claudeFixture(t, claudeMessageFixture("user", raw))
		report, err := IngestTranscript(context.Background(), opts)
		if err != nil || report.Stored != 1 {
			t.Fatalf("valid %s: %+v %v", raw, report, err)
		}
		files, err := filepath.Glob(filepath.Join(opts.CacheDir, "*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("cache: %v %v", files, err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var event TranscriptEvent
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Content != want {
			t.Fatalf("%s decoded %q want %q", raw, event.Content, want)
		}
	}
}

func TestTranscriptPreservesPairedAndLiteralEscapesInToolKeys(t *testing.T) {
	args := `{"\ud83d\ude42":{"literal":"\\ud800","pair":"\ud800\udc00"}}`
	claude := claudeFixture(t, claudeMessageFixture("assistant", `[{"type":"tool_use","id":"id","name":"tool","input":`+args+`}]`))
	antigravity := TranscriptIngestOptions{SourcePath: transcriptFixture(t, strings.Replace(transcriptEvent, `{"command":"printf fixture"}`, args, 1)), CacheDir: filepath.Join(t.TempDir(), "cache")}
	for _, opts := range []TranscriptIngestOptions{claude, antigravity} {
		report, err := IngestTranscript(context.Background(), opts)
		if err != nil || report.Stored != 1 {
			t.Fatalf("paired tool input: %+v %v", report, err)
		}
		files, err := filepath.Glob(filepath.Join(opts.CacheDir, "*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("cache: %v %v", files, err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var event TranscriptEvent
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		var nested map[string]string
		if err := json.Unmarshal(event.ToolCalls[0].Args["🙂"], &nested); err != nil {
			t.Fatal(err)
		}
		if nested["literal"] != `\ud800` || nested["pair"] != "𐀀" {
			t.Fatalf("changed tool input: %+v", nested)
		}
	}
}

func TestTranscriptUnicodeValidatorRejectsIncompleteEscapes(t *testing.T) {
	for _, content := range []string{`"\u12"`, `"\uZZZZ"`, `"\uD800\u12"`, "\"\\"} {
		opts := claudeFixture(t, claudeMessageFixture("user", content))
		if _, err := IngestTranscript(context.Background(), opts); err == nil {
			t.Fatalf("incomplete Unicode accepted: %s", content)
		}
	}
}
