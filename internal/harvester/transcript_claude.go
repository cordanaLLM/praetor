// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// ClaudeUsage describes token consumption of a Claude response.
type ClaudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// ClaudeOrigin describes the initiator of a Claude session record.
type ClaudeOrigin struct {
	Kind string `json:"kind"`
}

// ClaudeRecord represents a single decoded Claude Code JSONL envelope.
type ClaudeRecord struct {
	Type             string          `json:"type"`
	UUID             string          `json:"uuid,omitempty"`
	ParentUUID       string          `json:"parentUuid,omitempty"`
	SessionID        string          `json:"sessionId,omitempty"`
	Timestamp        string          `json:"timestamp,omitempty"`
	Source           string          `json:"source,omitempty"`
	Model            string          `json:"model,omitempty"`
	GitBranch        string          `json:"gitBranch,omitempty"`
	RequestID        string          `json:"requestId,omitempty"`
	MessageID        string          `json:"messageId,omitempty"`
	IsMeta           bool            `json:"isMeta,omitempty"`
	IsCompactSummary bool            `json:"isCompactSummary,omitempty"`
	IsSidechain      bool            `json:"isSidechain,omitempty"`
	HookName         string          `json:"hookName,omitempty"`
	HookEvent        string          `json:"hookEvent,omitempty"`
	ToolUseID        string          `json:"toolUseId,omitempty"`
	Origin           *ClaudeOrigin   `json:"origin,omitempty"`
	Message          json.RawMessage `json:"message,omitempty"`
	Usage            *ClaudeUsage    `json:"usage,omitempty"`
}

// ClaudeInnerMessage represents the inner message object inside ClaudeRecord.Message.
type ClaudeInnerMessage struct {
	ID               string          `json:"id,omitempty"`
	Role             string          `json:"role,omitempty"`
	Type             string          `json:"type,omitempty"`
	Model            string          `json:"model,omitempty"`
	Content          json.RawMessage `json:"content,omitempty"`
	Usage            *ClaudeUsage    `json:"usage,omitempty"`
	IsMeta           bool            `json:"isMeta,omitempty"`
	IsCompactSummary bool            `json:"isCompactSummary,omitempty"`
	IsSidechain      bool            `json:"isSidechain,omitempty"`
}

var canonicalClaudeEnvelopeFields = []string{
	"type", "uuid", "parentUuid", "sessionId", "timestamp", "message",
	"source", "model", "gitBranch", "requestId", "messageId",
	"isMeta", "isCompactSummary", "isSidechain",
	"hookName", "hookEvent", "toolUseId", "origin", "usage",
}

var canonicalClaudeMessageFields = []string{
	"id", "role", "type", "model", "content", "usage",
	"isMeta", "isCompactSummary", "isSidechain",
}

// DecodeClaudeLine decodes and validates one Claude Code JSONL envelope line.
func DecodeClaudeLine(raw []byte) (*ClaudeRecord, *ClaudeInnerMessage, error) {
	if !utf8.Valid(raw) {
		return nil, nil, fmt.Errorf("invalid UTF-8 in transcript line")
	}
	if err := rejectDuplicateTranscriptKeys(raw); err != nil {
		return nil, nil, err
	}
	var record ClaudeRecord
	if err := decodeClaudeObject(raw, &record, canonicalClaudeEnvelopeFields); err != nil {
		return nil, nil, err
	}
	var inner *ClaudeInnerMessage
	if len(record.Message) > 0 {
		var msg ClaudeInnerMessage
		if err := decodeClaudeObject(record.Message, &msg, canonicalClaudeMessageFields); err == nil {
			inner = &msg
		}
	}
	return &record, inner, nil
}

func parseClaudeRecord(raw []byte, source TranscriptSource, line int) (TranscriptEvent, error) {
	var record ClaudeRecord
	if err := decodeClaudeObject(raw, &record, canonicalClaudeEnvelopeFields); err != nil {
		return TranscriptEvent{}, err
	}
	event := TranscriptEvent{SourceFormat: source.Format, SourceSHA256: source.SHA256, Line: line, StepIndex: line - 1, SessionID: record.SessionID}
	if record.Type != "user" && record.Type != "assistant" {
		event.metadataRecord = true
		return event, validateClaudeMetadata(record.Type)
	}
	if err := validateClaudeEnvelope(record); err != nil {
		return TranscriptEvent{}, err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%s:%d:%s", source.Format, source.SHA256, line, record.UUID))
	event.Version, event.ID = transcriptCacheVersion, hex.EncodeToString(sum[:])
	event.RecordUUID, event.ParentUUID = record.UUID, record.ParentUUID
	event.Source, event.Type, event.Status = "claude-code", record.Type, "observed"
	event.CreatedAt = record.Timestamp
	if err := parseClaudeMessage(record.Message, record.Type, &event); err != nil {
		return TranscriptEvent{}, err
	}
	return event, nil
}

func validateClaudeEnvelope(record ClaudeRecord) error {
	if record.UUID == "" || record.SessionID == "" {
		return fmt.Errorf("claude message requires uuid and sessionId")
	}
	if len(record.UUID) > 256 || len(record.ParentUUID) > 256 || len(record.SessionID) > 256 {
		return fmt.Errorf("claude identity exceeds 256 bytes")
	}
	if _, err := time.Parse(time.RFC3339, record.Timestamp); err != nil {
		return fmt.Errorf("claude timestamp must be RFC3339")
	}
	return nil
}

func validateClaudeMetadata(kind string) error {
	switch kind {
	case "bridge-session", "queue-operation", "attachment", "file-history-snapshot", "atis-latch", "last-prompt", "ai-title", "file-history-delta", "system", "mode", "history-suppression", "frame-link", "artifact-comment-monitor", "artifact-autoreact-ledger", "progress", "summary":
		return nil
	default:
		return fmt.Errorf("unsupported claude record type")
	}
}

// Decode only known fields but reject case folding; unrelated versioned envelope
// metadata is deliberately ignored. Duplicate keys were checked before dispatch.
func decodeClaudeObject(raw []byte, destination any, fields []string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("claude value must be an object: %w", err)
	}
	if object == nil {
		return fmt.Errorf("claude value must be an object")
	}
	if err := rejectTranscriptAliases(object, fields); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("invalid Claude object: %w", err)
	}
	return nil
}

func claudeConversation(source *TranscriptSource, events []TranscriptEvent) error {
	source.Conversation = ""
	for _, event := range events {
		if event.SessionID == "" {
			continue
		}
		if source.Conversation == "" {
			source.Conversation = event.SessionID
		}
		if event.SessionID != source.Conversation {
			return fmt.Errorf("claude source contains multiple session IDs")
		}
	}
	return nil
}
