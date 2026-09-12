// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const maxClaudeContentBlocks = 128

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeBlock struct {
	ToolName  string                     `json:"tool_name"`
	Type      string                     `json:"type"`
	Text      *string                    `json:"text"`
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Input     map[string]json.RawMessage `json:"input"`
	ToolUseID string                     `json:"tool_use_id"`
	Content   json.RawMessage            `json:"content"`
	IsError   json.RawMessage            `json:"is_error"`
}

func parseClaudeMessage(raw []byte, role string, event *TranscriptEvent) error {
	var message claudeMessage
	if err := decodeClaudeObject(raw, &message, []string{"role", "content"}); err != nil {
		return err
	}
	if message.Role != role {
		return fmt.Errorf("claude message role differs from record type")
	}
	content := bytes.TrimSpace(message.Content)
	if len(content) > 0 && content[0] == '"' {
		return json.Unmarshal(content, &event.Content)
	}
	blocks, err := decodeClaudeBlocks(content)
	if err != nil {
		return err
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if err := acceptClaudeBlock(block, role, event, &texts); err != nil {
			return err
		}
	}
	event.Content = strings.Join(texts, "\n")
	return nil
}

func decodeClaudeBlocks(raw []byte) ([]claudeBlock, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("claude content must be text or a block array: %w", err)
	}
	if entries == nil || len(entries) > maxClaudeContentBlocks {
		return nil, fmt.Errorf("claude content requires at most %d blocks", maxClaudeContentBlocks)
	}
	blocks := make([]claudeBlock, 0, len(entries))
	fields := []string{"type", "text", "id", "name", "input", "tool_use_id", "content", "is_error", "thinking", "signature", "tool_name"}
	for _, raw := range entries {
		var block claudeBlock
		if err := decodeClaudeObject(raw, &block, fields); err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func acceptClaudeBlock(block claudeBlock, role string, event *TranscriptEvent, texts *[]string) error {
	switch block.Type {
	case "text":
		if block.Text == nil {
			return fmt.Errorf("claude text block requires text")
		}
		*texts = append(*texts, *block.Text)
	case "thinking", "redacted_thinking":
		if role != "assistant" {
			return fmt.Errorf("claude thinking block requires assistant role")
		}
		event.thinkingBlocks++
	case "tool_use":
		if role != "assistant" || block.ID == "" || block.Name == "" || block.Input == nil {
			return fmt.Errorf("claude tool_use requires assistant role, id, name and object input")
		}
		event.ToolCalls = append(event.ToolCalls, TranscriptToolCall{ID: block.ID, Name: block.Name, Args: block.Input})
	case "tool_result":
		return acceptClaudeToolResult(block, role, event)
	default:
		return fmt.Errorf("unsupported Claude content block %q", block.Type)
	}
	return nil
}

func acceptClaudeToolResult(block claudeBlock, role string, event *TranscriptEvent) error {
	if role != "user" || block.ToolUseID == "" {
		return fmt.Errorf("claude tool_result requires user role and tool_use_id")
	}
	result := TranscriptToolResult{ToolUseID: block.ToolUseID}
	if len(block.IsError) > 0 {
		if string(block.IsError) == "null" {
			return fmt.Errorf("claude is_error must be boolean")
		}
		if err := json.Unmarshal(block.IsError, &result.IsError); err != nil {
			return err
		}
	}
	content := bytes.TrimSpace(block.Content)
	if len(content) > 0 && content[0] == '"' {
		if err := json.Unmarshal(content, &result.Content); err != nil {
			return err
		}
	} else {
		if err := claudeResultContent(content, &result); err != nil {
			return err
		}
	}
	event.ToolResults = append(event.ToolResults, result)
	return nil
}

// Tool results preserve text and tool references observed in Claude ToolSearch
// replies. Other nested blocks fail instead of being recursively interpreted.
func claudeResultContent(content []byte, result *TranscriptToolResult) error {
	blocks, err := decodeClaudeBlocks(content)
	if err != nil {
		return err
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text == nil {
				return fmt.Errorf("claude result text requires text")
			}
			texts = append(texts, *block.Text)
		case "tool_reference":
			if block.ToolName == "" {
				return fmt.Errorf("claude tool_reference requires tool_name")
			}
			result.ToolReferences = append(result.ToolReferences, block.ToolName)
		default:
			return fmt.Errorf("unsupported Claude tool_result content block %q", block.Type)
		}
	}
	result.Content = strings.Join(texts, "\n")
	return nil
}
