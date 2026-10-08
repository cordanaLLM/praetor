// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/harvester"
)

type spendReader struct {
	classifier *Classifier
	attr       attribution
	report     *SpendReport
	limits     sourceLimits
}

func (s *spendReader) jsonLines(ctx context.Context, in io.Reader) error {
	limits := harvester.ScanLimits{MaxRecords: s.limits.Lines, MaxBytes: s.limits.Bytes}
	return harvester.ScanTranscriptLines(ctx, in, limits, func(line int, raw []byte) error {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return nil
		}
		var entry rawSpendEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		s.report.process(&entry, s.classifier, s.attr)
		return nil
	})
}

// jsonArray streams a top-level JSON array of entries.
func (s *spendReader) jsonArray(ctx context.Context, in io.Reader) error {
	counter := &harvester.CountingReader{Reader: in}
	decoder := json.NewDecoder(counter)
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("read JSON array start: %w", err)
	}
	count := 0
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > s.limits.Lines {
			return fmt.Errorf("exceeds %d entries limit", s.limits.Lines)
		}
		if counter.Count > s.limits.Bytes {
			return fmt.Errorf("exceeds %d byte limit", s.limits.Bytes)
		}
		var entry rawSpendEntry
		if err := decoder.Decode(&entry); err != nil {
			return fmt.Errorf("entry %d: %w", count, err)
		}
		s.report.process(&entry, s.classifier, s.attr)
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("read JSON array end: %w", err)
	}
	if counter.Count > s.limits.Bytes {
		return fmt.Errorf("exceeds %d byte limit", s.limits.Bytes)
	}
	return nil
}

func csvHeader(header []string) (map[string]int, error) {
	indices := make(map[string]int, len(header))
	for i, h := range header {
		indices[strings.TrimSpace(h)] = i
	}
	for _, column := range []string{"spend", "model", "model_group"} {
		if _, ok := indices[column]; ok {
			return indices, nil
		}
	}
	return nil, fmt.Errorf("CSV header must include a spend, model or model_group column, got %v", header)
}

func csvField(record []string, indices map[string]int, field string) string {
	if idx, ok := indices[field]; ok && idx < len(record) {
		return strings.TrimSpace(record[idx])
	}
	return ""
}

func csvInt(record []string, indices map[string]int, field string) int64 {
	n, err := strconv.ParseInt(csvField(record, indices, field), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func csvJSONText(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(strconv.Quote(value))
}

func csvEntry(record []string, indices map[string]int) rawSpendEntry {
	spend, err := strconv.ParseFloat(csvField(record, indices, "spend"), 64)
	if err != nil {
		spend = 0
	}
	return rawSpendEntry{
		RequestID:        csvField(record, indices, "request_id"),
		Model:            csvField(record, indices, "model"),
		ModelGroup:       csvField(record, indices, "model_group"),
		Spend:            spend,
		TotalTokens:      csvInt(record, indices, "total_tokens"),
		PromptTokens:     csvInt(record, indices, "prompt_tokens"),
		CompletionTokens: csvInt(record, indices, "completion_tokens"),
		StartTime:        csvField(record, indices, "startTime"),
		RequestTags:      csvJSONText(csvField(record, indices, "request_tags")),
		Metadata:         csvJSONText(csvField(record, indices, "metadata")),
		Branch:           csvField(record, indices, "branch"),
		PullRequest:      csvJSONText(csvField(record, indices, "pull_request")),
		PR:               csvJSONText(csvField(record, indices, "pr")),
		Tags:             csvJSONText(csvField(record, indices, "tags")),
	}
}

func (s *spendReader) csvRows(ctx context.Context, in io.Reader) error {
	counter := &harvester.CountingReader{Reader: in}
	reader := csv.NewReader(counter)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}
	indices, err := csvHeader(header)
	if err != nil {
		return err
	}
	for row := 1; row <= s.limits.Lines+1; row++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("CSV row %d: %w", row, err)
		}
		if row > s.limits.Lines {
			return fmt.Errorf("exceeds %d rows limit", s.limits.Lines)
		}
		if counter.Count > s.limits.Bytes {
			return fmt.Errorf("exceeds %d byte limit", s.limits.Bytes)
		}
		entry := csvEntry(record, indices)
		s.report.process(&entry, s.classifier, s.attr)
	}
	return fmt.Errorf("exceeds %d rows limit", s.limits.Lines)
}

// sniffFormat picks the reader from the extension, else from the first byte.
func sniffFormat(path string, in *bufio.Reader) string {
	lower := strings.ToLower(path)
	for _, ext := range []string{".jsonl", ".csv", ".json"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	peek, err := in.Peek(512)
	if err != nil && len(peek) == 0 {
		return ".csv"
	}
	trimmed := bytes.TrimSpace(peek)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '[':
		return ".json"
	case len(trimmed) > 0 && trimmed[0] == '{':
		return ".jsonl"
	default:
		return ".csv"
	}
}

func (s *spendReader) read(ctx context.Context, path string, in io.Reader) error {
	buffered := bufio.NewReader(in)
	switch sniffFormat(path, buffered) {
	case ".jsonl":
		return s.jsonLines(ctx, buffered)
	case ".json":
		return s.jsonArray(ctx, buffered)
	default:
		return s.csvRows(ctx, buffered)
	}
}

// ReadSpendLogFile reads a LiteLLM_SpendLogs export (JSON lines, JSON array or CSV) and
// attributes each entry to a pull request. Every read error and limit overflow is an error.
func ReadSpendLogFile(ctx context.Context, path string, prs []forge.MergedPullRequest, classifier *Classifier) (*SpendReport, error) {
	return readSpendLogFile(ctx, path, prs, classifier, defaultLimits())
}

func readSpendLogFile(ctx context.Context, path string, prs []forge.MergedPullRequest, classifier *Classifier, limits sourceLimits) (report *SpendReport, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, errors.New("empty spend log path")
	}
	f, err := os.Open(cleanPath) // #nosec G304 -- path is configured in manifest or CLI flag.
	if err != nil {
		return nil, fmt.Errorf("open spend log file %s: %w", cleanPath, err)
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	if info, statErr := f.Stat(); statErr == nil && info.Size() > limits.Bytes {
		return nil, fmt.Errorf("spend log file %s exceeds %d byte limit", cleanPath, limits.Bytes)
	}
	reader := &spendReader{classifier: classifier, attr: buildAttribution(prs), report: newSpendReport(), limits: limits}
	if err := reader.read(ctx, cleanPath, f); err != nil {
		return nil, fmt.Errorf("read spend log file %s: %w", cleanPath, err)
	}
	return reader.report, nil
}
