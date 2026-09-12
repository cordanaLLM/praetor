// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import "encoding/json"

const (
	// MaxTranscriptSourceBytes bounds a complete immutable source snapshot.
	MaxTranscriptSourceBytes = 64 * 1024 * 1024
	// MaxTranscriptRecords bounds physical JSONL records; overflow is an error.
	MaxTranscriptRecords = 100000
	// MaxTranscriptBatchRecords bounds a single ingestion call; zero selects 1000.
	MaxTranscriptBatchRecords = 10000
	transcriptDefaultBatch    = 1000
	transcriptCacheVersion    = 1
)

// TranscriptIngestOptions explicitly selects a local source and private cache.
// Cursor is opaque and belongs to exactly one source snapshot. No network is used.
type TranscriptIngestOptions struct {
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	SourcePath     string `json:"source_path"`
	CacheDir       string `json:"cache_dir"`
	Cursor         string `json:"cursor,omitempty"`
	MaxRecords     int    `json:"max_records,omitempty"`
}

// TranscriptSource identifies the immutable bytes used for this ingestion call.
type TranscriptSource struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
	Format       string `json:"format"`
	Conversation string `json:"conversation"`
}

// TranscriptToolCall is observed untrusted input, never an executable instruction.
type TranscriptToolCall struct {
	Name string                     `json:"name"`
	Args map[string]json.RawMessage `json:"args"`
}

// TranscriptEvent preserves observed content and provenance, not verified facts.
// Internal thinking is deliberately excluded. IDs bind source bytes and line/step.
type TranscriptEvent struct {
	Version         int                  `json:"version"`
	ID              string               `json:"id"`
	SourceSHA256    string               `json:"source_sha256"`
	Line            int                  `json:"line"`
	StepIndex       int                  `json:"step_index"`
	Source          string               `json:"source"`
	Type            string               `json:"type"`
	Status          string               `json:"status"`
	CreatedAt       string               `json:"created_at"`
	Content         string               `json:"content,omitempty"`
	ToolCalls       []TranscriptToolCall `json:"tool_calls,omitempty"`
	ExitCode        *int                 `json:"exit_code,omitempty"`
	TruncatedFields []string             `json:"truncated_fields,omitempty"`
	Error           string               `json:"error,omitempty"`
}

// TranscriptIngestReport contains metadata only. On cache failure it reports actual
// writes and a retry cursor for the original batch; successful retries deduplicate.
type TranscriptIngestReport struct {
	Source           TranscriptSource `json:"source"`
	TruncatedRecords int              `json:"truncated_records"`
	TotalRecords     int              `json:"total_records"`
	Scanned          int              `json:"scanned"`
	Stored           int              `json:"stored"`
	AlreadyPresent   int              `json:"already_present"`
	Skipped          int              `json:"skipped"`
	Complete         bool             `json:"complete"`
	Remaining        int              `json:"remaining"`
	NextCursor       string           `json:"next_cursor,omitempty"`
}
