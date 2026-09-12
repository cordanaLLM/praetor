// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type transcriptCursor struct {
	Version  int    `json:"version"`
	SHA256   string `json:"source_sha256"`
	CacheKey string `json:"cache_key"`
	NextLine int    `json:"next_line"`
}

// IngestTranscript validates a stable Antigravity source, then persists one bounded
// batch of observed events in an explicit private cache. It never calls external
// services or executes tool arguments. Existing harvest memory behavior is unchanged.
func IngestTranscript(ctx context.Context, opts TranscriptIngestOptions) (*TranscriptIngestReport, error) {
	if err := validateTranscriptOptions(ctx, &opts); err != nil {
		return nil, err
	}
	ctx, cancel := boundedTranscriptContext(ctx)
	defer cancel()
	source, events, err := loadTranscriptEvents(ctx, opts)
	if err != nil {
		return nil, err
	}
	opts.CacheDir, err = transcriptDestination(opts.CacheDir, source.Path)
	if err != nil {
		return nil, err
	}
	start, err := decodeTranscriptCursor(opts.Cursor, source.SHA256, transcriptCacheKey(opts.CacheDir), len(events))
	if err != nil {
		return nil, err
	}
	report, err := newTranscriptReport(source, events, start, opts.CacheDir)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		report.Complete = true
		report.NextCursor = ""
		return report, nil
	}
	root, err := openBundleRoot(opts.CacheDir)
	if err != nil {
		return report, fmt.Errorf("open transcript cache: %w", err)
	}
	err = verifyTranscriptPrefix(ctx, root, events, start)
	if err == nil {
		err = ingestTranscriptBatch(ctx, root, events, start, opts.MaxRecords, report)
	}
	return report, errors.Join(err, root.Close())
}

func validateTranscriptOptions(ctx context.Context, opts *TranscriptIngestOptions) error {
	if ctx == nil {
		return fmt.Errorf("transcript ingestion requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.CacheDir == "" {
		return fmt.Errorf("transcript cache directory is required")
	}
	if opts.MaxRecords < 0 || opts.MaxRecords > MaxTranscriptBatchRecords {
		return fmt.Errorf("transcript batch must be between 1 and %d", MaxTranscriptBatchRecords)
	}
	if opts.MaxRecords == 0 {
		opts.MaxRecords = transcriptDefaultBatch
	}
	return nil
}

func decodeTranscriptCursor(raw, sum, cacheKey string, total int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > 1024 {
		return 0, fmt.Errorf("transcript cursor too long")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid transcript cursor: %w", err)
	}
	var cursor transcriptCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return 0, fmt.Errorf("invalid transcript cursor: %w", err)
	}
	if cursor.Version != transcriptCacheVersion || cursor.SHA256 != sum || cursor.CacheKey != cacheKey || cursor.NextLine < 1 || cursor.NextLine > total+1 {
		return 0, fmt.Errorf("transcript cursor does not match source snapshot or range")
	}
	return cursor.NextLine - 1, nil
}

func encodeTranscriptCursor(sum, cacheKey string, start int) (string, error) {
	data, err := json.Marshal(transcriptCursor{Version: transcriptCacheVersion, SHA256: sum, CacheKey: cacheKey, NextLine: start + 1})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func ingestTranscriptBatch(ctx context.Context, root *os.Root, events []TranscriptEvent, start, limit int, report *TranscriptIngestReport) error {
	end := min(start+limit, len(events))
	for index := start; index < end; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := events[index]
		if !hasTranscriptPayload(event) {
			report.Skipped++
		} else if err := storeTranscriptEvent(root, event, report); err != nil {
			return fmt.Errorf("cache transcript line %d: %w", event.Line, err)
		}
		report.Scanned++
	}
	report.Remaining = len(events) - end
	report.Complete = end == len(events)
	if report.Complete {
		report.NextCursor = ""
		return nil
	}
	cursor, err := encodeTranscriptCursor(report.Source.SHA256, transcriptCacheKey(root.Name()), end)
	report.NextCursor = cursor
	return err
}

func loadTranscriptEvents(ctx context.Context, opts TranscriptIngestOptions) (TranscriptSource, []TranscriptEvent, error) {
	data, source, err := snapshotTranscript(ctx, opts.SourcePath)
	if err != nil {
		return source, nil, err
	}
	if opts.ExpectedSHA256 != "" && opts.ExpectedSHA256 != source.SHA256 {
		return source, nil, fmt.Errorf("transcript source differs from expected SHA256")
	}
	events, err := parseTranscript(ctx, data, source)
	return source, events, err
}

func newTranscriptReport(source TranscriptSource, events []TranscriptEvent, start int, cache string) (*TranscriptIngestReport, error) {
	report := &TranscriptIngestReport{Source: source, TotalRecords: len(events), Remaining: len(events) - start}
	for _, event := range events {
		if len(event.TruncatedFields) > 0 {
			report.TruncatedRecords++
		}
	}
	cursor, err := encodeTranscriptCursor(source.SHA256, transcriptCacheKey(cache), start)
	report.NextCursor = cursor
	return report, err
}

func transcriptCacheKey(directory string) string {
	sum := sha256.Sum256([]byte(directory))
	return hex.EncodeToString(sum[:])
}

func transcriptDestination(directory, source string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absolute, source)
	if err != nil {
		return "", err
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("transcript cache must not be the source or one of its ancestors")
	}
	if err := rejectBundleLinks(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func boundedTranscriptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Minute)
}
