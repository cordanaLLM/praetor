// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

type antigravityRecord struct {
	StepIndex       *int                 `json:"step_index"`
	Source          string               `json:"source"`
	Type            string               `json:"type"`
	Status          string               `json:"status"`
	CreatedAt       string               `json:"created_at"`
	Content         string               `json:"content"`
	ToolCalls       []TranscriptToolCall `json:"tool_calls"`
	ExitCode        *int                 `json:"exit_code"`
	TruncatedFields []string             `json:"truncated_fields"`
	Error           string               `json:"error"`
}

func parseTranscript(ctx context.Context, data []byte, source TranscriptSource) ([]TranscriptEvent, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), MaxTranscriptLineBytes+1)
	events := make([]TranscriptEvent, 0)
	for line := 1; scanner.Scan(); line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if line > MaxTranscriptRecords {
			return nil, fmt.Errorf("transcript exceeds %d record bound", MaxTranscriptRecords)
		}
		event, err := parseTranscriptEvent(scanner.Bytes(), source, line)
		if err != nil {
			return nil, fmt.Errorf("transcript line %d: %w", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}
	return events, nil
}

func parseTranscriptEvent(raw []byte, source TranscriptSource, line int) (TranscriptEvent, error) {
	var event TranscriptEvent
	if !utf8.Valid(raw) {
		return event, fmt.Errorf("invalid UTF-8")
	}
	if err := rejectDuplicateTranscriptKeys(raw); err != nil {
		return event, err
	}
	if err := rejectTranscriptFieldAliases(raw); err != nil {
		return event, err
	}
	var record antigravityRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return event, fmt.Errorf("invalid Antigravity JSON: %w", err)
	}
	if err := validateTranscriptRecord(record); err != nil {
		return event, err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d:%d", source.SHA256, line, *record.StepIndex))
	event = TranscriptEvent{Version: transcriptCacheVersion, ID: hex.EncodeToString(sum[:]), SourceSHA256: source.SHA256, Line: line, StepIndex: *record.StepIndex,
		Source: record.Source, Type: record.Type, Status: record.Status, CreatedAt: record.CreatedAt, Content: record.Content, ToolCalls: record.ToolCalls, ExitCode: record.ExitCode, Error: record.Error, TruncatedFields: record.TruncatedFields}
	return event, nil
}

func validateTranscriptRecord(record antigravityRecord) error {
	if record.StepIndex == nil || *record.StepIndex < 0 {
		return fmt.Errorf("missing or invalid step_index")
	}
	if record.Source == "" || record.Type == "" || record.Status == "" {
		return fmt.Errorf("missing Antigravity event envelope")
	}
	if _, err := time.Parse(time.RFC3339, record.CreatedAt); err != nil {
		return fmt.Errorf("created_at must be an RFC3339 timestamp")
	}
	if len(record.ToolCalls) > 128 {
		return fmt.Errorf("tool call count exceeds 128")
	}
	for _, tool := range record.ToolCalls {
		if tool.Name == "" || tool.Args == nil {
			return fmt.Errorf("tool call requires name and object args")
		}
	}
	return nil
}
