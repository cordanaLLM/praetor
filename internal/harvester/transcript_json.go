// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const transcriptJSONTokenLimit = 1000000
const transcriptJSONDepthLimit = 128

type transcriptJSONFrame struct {
	object      bool
	keyExpected bool
	keys        map[string]struct{}
}

// JSON duplicate keys are ambiguous source evidence: Unmarshal otherwise silently
// discards the earlier value. Token scanning also covers nested untrusted tool args.
func rejectDuplicateTranscriptKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	stack := make([]transcriptJSONFrame, 0)
	for count := 0; count < transcriptJSONTokenLimit; count++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid JSON token stream: %w", err)
		}
		stack, err = acceptTranscriptToken(stack, token)
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("transcript JSON token bound exceeded")
}

func acceptTranscriptToken(stack []transcriptJSONFrame, token json.Token) ([]transcriptJSONFrame, error) {
	delimiter, isDelimiter := token.(json.Delim)
	if isDelimiter && (delimiter == '}' || delimiter == ']') {
		return stack[:len(stack)-1], nil
	}
	if len(stack) > 0 {
		frame := &stack[len(stack)-1]
		if frame.object && frame.keyExpected {
			key, ok := token.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := frame.keys[key]; duplicate {
				return nil, fmt.Errorf("duplicate JSON object key")
			}
			frame.keys[key] = struct{}{}
			frame.keyExpected = false
			return stack, nil
		}
		frame.keyExpected = frame.object
	}
	if !isDelimiter {
		return stack, nil
	}
	if len(stack) >= transcriptJSONDepthLimit {
		return nil, fmt.Errorf("transcript JSON nesting bound exceeded")
	}
	stack = append(stack, transcriptJSONFrame{object: delimiter == '{', keyExpected: delimiter == '{', keys: make(map[string]struct{})})
	return stack, nil
}

// Struct decoding folds JSON field names; require canonical envelope spellings.
// Tool argument object keys remain case-sensitive and are preserved without folding.
func rejectTranscriptFieldAliases(raw []byte) error {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("invalid transcript object: %w", err)
	}
	fields := []string{"step_index", "source", "type", "status", "created_at", "content", "tool_calls", "exit_code", "error", "truncated_fields", "thinking", "error_code"}
	if err := rejectTranscriptAliases(record, fields); err != nil {
		return err
	}
	rawTools, exists := record["tool_calls"]
	if !exists {
		return nil
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return fmt.Errorf("tool_calls must contain objects: %w", err)
	}
	if len(tools) > 128 {
		return fmt.Errorf("tool call count exceeds 128")
	}
	for _, tool := range tools {
		if err := rejectTranscriptAliases(tool, []string{"name", "args"}); err != nil {
			return err
		}
	}
	return nil
}

func rejectTranscriptAliases(object map[string]json.RawMessage, canonical []string) error {
	for key := range object {
		for _, field := range canonical {
			if key != field && strings.EqualFold(key, field) {
				return fmt.Errorf("transcript envelope contains a noncanonical field spelling")
			}
		}
	}
	return nil
}
