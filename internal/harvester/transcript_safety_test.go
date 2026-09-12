// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIngestTranscriptRejectsCacheSymlinksAndConflicts(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent)
	cache := t.TempDir()
	options := TranscriptIngestOptions{SourcePath: source, CacheDir: cache}
	first, err := IngestTranscript(context.Background(), options)
	if err != nil || first.Stored != 1 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	files, err := filepath.Glob(filepath.Join(cache, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if err := os.WriteFile(files[0], []byte("preserve corrupt evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestTranscript(context.Background(), options); err == nil {
		t.Fatal("corrupt cache overwritten")
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || string(raw) != "preserve corrupt evidence" {
		t.Fatal("corrupt evidence changed", err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, files[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestTranscript(context.Background(), options); err == nil {
		t.Fatal("cache symlink accepted")
	}
	raw, err = os.ReadFile(outside)
	if err != nil || string(raw) != "protected" {
		t.Fatal("outside changed", err)
	}
}

func TestIngestTranscriptRejectsSourceAndDestinationLinkAncestors(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent)
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(filepath.Dir(source), link); err != nil {
		t.Fatal(err)
	}
	opts := TranscriptIngestOptions{SourcePath: filepath.Join(link, filepath.Base(source)), CacheDir: t.TempDir()}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("source ancestor symlink accepted")
	}
	opts.SourcePath = source
	opts.CacheDir = filepath.Join(link, "cache")
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("destination ancestor symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(source), "cache")); !os.IsNotExist(err) {
		t.Fatal("source tree mutated", err)
	}
}

func TestIngestTranscriptSourceAndLineBounds(t *testing.T) {
	source := transcriptFixture(t, "")
	file, err := os.OpenFile(source, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxTranscriptSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: filepath.Join(t.TempDir(), "cache")}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("oversized source accepted")
	}
	oversized := strings.ReplaceAll(transcriptEvent, "fixture tool result", strings.Repeat("x", MaxTranscriptLineBytes))
	if err := os.WriteFile(source, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("oversized line accepted")
	}
	if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
		t.Fatal("bounded validation wrote cache", err)
	}
}

func TestIngestTranscriptExplicitTruncationAndCopiedSource(t *testing.T) {
	body := strings.Replace(transcriptEvent, `"step_index":7`, `"truncated_fields":["content"],"step_index":7`, 1)
	source := transcriptFixture(t, body)
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: t.TempDir()}
	first, err := IngestTranscript(context.Background(), opts)
	if err != nil || first.TruncatedRecords != 1 {
		t.Fatalf("truncation missing: %+v, %v", first, err)
	}
	opts.SourcePath = transcriptFixture(t, body)
	second, err := IngestTranscript(context.Background(), opts)
	if err != nil || second.AlreadyPresent != 1 || second.Stored != 0 {
		t.Fatalf("copied source duplicated: %+v, %v", second, err)
	}
	files, err := filepath.Glob(filepath.Join(opts.CacheDir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var event TranscriptEvent
	if err := json.Unmarshal(raw, &event); err != nil || len(event.TruncatedFields) != 1 {
		t.Fatal("truncation declaration lost", err)
	}
}

type cancelAfterTranscriptWrite struct {
	context.Context
	cache  string
	cancel context.CancelFunc
}

func (c cancelAfterTranscriptWrite) Err() error {
	files, err := filepath.Glob(filepath.Join(c.cache, "*.json"))
	if err != nil || len(files) > 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestIngestTranscriptPartialCancellationIsRetryable(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent+transcriptEvent)
	cache := t.TempDir()
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx := cancelAfterTranscriptWrite{Context: parent, cache: cache, cancel: cancel}
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: cache}
	partial, err := IngestTranscript(ctx, opts)
	if err == nil || partial.Stored != 1 || partial.Complete || partial.NextCursor == "" {
		t.Fatalf("partial: %+v, %v", partial, err)
	}
	opts.Cursor = partial.NextCursor
	retry, err := IngestTranscript(context.Background(), opts)
	if err != nil || !retry.Complete || retry.Stored != 1 || retry.AlreadyPresent != 1 {
		t.Fatalf("retry: %+v, %v", retry, err)
	}
}

func TestIngestTranscriptConcurrentReplay(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent)
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: t.TempDir()}
	var wg sync.WaitGroup
	reports := make(chan *TranscriptIngestReport, 4)
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report, err := IngestTranscript(context.Background(), opts)
			reports <- report
			failures <- err
		}()
	}
	wg.Wait()
	close(reports)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored, present := 0, 0
	for report := range reports {
		stored += report.Stored
		present += report.AlreadyPresent
	}
	if stored != 1 || present != 3 {
		t.Fatalf("concurrent duplication: stored=%d present=%d", stored, present)
	}
}

func TestIngestTranscriptRejectsExpectedHashBeforeMutation(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent)
	cache := filepath.Join(t.TempDir(), "untouched")
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: cache, ExpectedSHA256: strings.Repeat("0", 64)}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("wrong original source accepted")
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("source mismatch wrote cache", err)
	}
}

func TestIngestTranscriptCursorRequiresDestinationPrefix(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent+transcriptEvent)
	cache := t.TempDir()
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: cache, MaxRecords: 1}
	first, err := IngestTranscript(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Cursor = first.NextCursor
	opts.CacheDir = t.TempDir()
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("cursor resumed into unrelated cache")
	}
	opts.CacheDir = cache
	files, err := filepath.Glob(filepath.Join(cache, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("cursor skipped missing prefix")
	}
}

func TestIngestTranscriptRejectsSourceAncestorAsCache(t *testing.T) {
	source := transcriptFixture(t, transcriptEvent)
	directory := filepath.Dir(source)
	if err := os.Chmod(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	opts := TranscriptIngestOptions{SourcePath: source, CacheDir: directory}
	if _, err := IngestTranscript(context.Background(), opts); err == nil {
		t.Fatal("source ancestor accepted")
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("original mode changed to %o", info.Mode().Perm())
	}
}

func TestIngestTranscriptRejectsDuplicateKeys(t *testing.T) {
	for _, body := range []string{
		strings.Replace(transcriptEvent, `"content":"fixture tool result"`, `"content":"first payload","content":"second payload"`, 1),
		strings.Replace(transcriptEvent, `"command":"printf fixture"`, `"command":"first","command":"second"`, 1),
	} {
		source := transcriptFixture(t, body)
		cache := filepath.Join(t.TempDir(), "untouched")
		if _, err := IngestTranscript(context.Background(), TranscriptIngestOptions{SourcePath: source, CacheDir: cache}); err == nil {
			t.Fatal("duplicate keys accepted")
		}
		if _, err := os.Stat(cache); !os.IsNotExist(err) {
			t.Fatal("ambiguous source wrote cache", err)
		}
	}
}

func TestIngestTranscriptRejectsEnvelopeCaseAliases(t *testing.T) {
	for _, body := range []string{
		strings.Replace(transcriptEvent, `"content":"fixture tool result"`, `"content":"first","Content":"second"`, 1),
		strings.Replace(transcriptEvent, `"name":"run_command"`, `"name":"first","Name":"second"`, 1),
		strings.Replace(transcriptEvent, `"args":`, `"Args":`, 1),
	} {
		source := transcriptFixture(t, body)
		opts := TranscriptIngestOptions{SourcePath: source, CacheDir: filepath.Join(t.TempDir(), "untouched")}
		if _, err := IngestTranscript(context.Background(), opts); err == nil {
			t.Fatal("case alias accepted")
		}
		if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
			t.Fatal("case alias mutated cache", err)
		}
	}
}

func TestIngestTranscriptPreservesCaseSensitiveToolArguments(t *testing.T) {
	body := strings.Replace(transcriptEvent, `"command":"printf fixture"`, `"command":"first","Command":"second"`, 1)
	opts := TranscriptIngestOptions{SourcePath: transcriptFixture(t, body), CacheDir: t.TempDir()}
	report, err := IngestTranscript(context.Background(), opts)
	if err != nil || report.Stored != 1 {
		t.Fatalf("valid case-sensitive args rejected: %+v, %v", report, err)
	}
	files, err := filepath.Glob(filepath.Join(opts.CacheDir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var event TranscriptEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if len(event.ToolCalls[0].Args) != 2 {
		t.Fatal("case-sensitive argument was dropped")
	}
}
