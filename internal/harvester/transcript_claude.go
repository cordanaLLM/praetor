// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type claudeRecord struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  string          `json:"timestamp"`
	Message    json.RawMessage `json:"message"`
}

func parseClaudeRecord(raw []byte, source TranscriptSource, line int) (TranscriptEvent, error) {
	var record claudeRecord
	if err := decodeClaudeObject(raw, &record, []string{"type", "uuid", "parentUuid", "sessionId", "timestamp", "message"}); err != nil {
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

func validateClaudeEnvelope(record claudeRecord) error {
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
