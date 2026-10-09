// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// LedgerUsage is the token usage block of an agent session response.
type LedgerUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// LedgerOrigin names the initiator of a session record.
type LedgerOrigin struct {
	Kind string `json:"kind"`
}

// LedgerMessage is the message object of a session record.
type LedgerMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *LedgerUsage    `json:"usage"`
}

// LedgerRecord is the part of a session record the efficiency ledger reads. It extends the
// envelope of the ingest decoder with branch, request, origin and flag fields.
type LedgerRecord struct {
	Type             string          `json:"type"`
	SessionID        string          `json:"sessionId"`
	Timestamp        string          `json:"timestamp"`
	GitBranch        string          `json:"gitBranch"`
	RequestID        string          `json:"requestId"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	IsSidechain      bool            `json:"isSidechain"`
	Origin           *LedgerOrigin   `json:"origin"`
	RawMessage       json.RawMessage `json:"message"`
	Message          *LedgerMessage  `json:"-"`
}

var ledgerEnvelopeFields = []string{
	"type", "sessionId", "timestamp", "gitBranch", "requestId",
	"isMeta", "isCompactSummary", "isSidechain", "origin", "message",
}

var ledgerMessageFields = []string{"id", "role", "model", "content", "usage"}

// DecodeLedgerLine applies the ingest checks (UTF-8, surrogates, duplicate keys, field
// spelling) to one session line and decodes the ledger fields. Other fields, such as the
// system record key toolUseID, are not part of the ledger envelope and are ignored.
func DecodeLedgerLine(raw []byte) (*LedgerRecord, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	if err := rejectUnpairedTranscriptSurrogates(raw); err != nil {
		return nil, err
	}
	if err := rejectDuplicateTranscriptKeys(raw); err != nil {
		return nil, err
	}
	var record LedgerRecord
	if err := decodeClaudeObject(raw, &record, ledgerEnvelopeFields); err != nil {
		return nil, err
	}
	if len(record.RawMessage) == 0 || string(record.RawMessage) == "null" {
		return &record, nil
	}
	var message LedgerMessage
	if err := decodeClaudeObject(record.RawMessage, &message, ledgerMessageFields); err != nil {
		return nil, fmt.Errorf("message: %w", err)
	}
	record.Message = &message
	return &record, nil
}
