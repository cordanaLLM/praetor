package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxClaudeTranscriptBytes     = 64 << 20
	maxClaudeTranscriptLines     = 100_000
	maxClaudeTranscriptLineBytes = 8 << 20
)

var claudeAgentIDShape = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// readClaudeUsage sums all assistant usage rows, including cache creation and reads,
// from the subagent transcript reported on SubagentStop.
func readClaudeUsage(ctx context.Context, transcriptPath, agentID string) (int64, error) {
	data, err := claudeTranscriptData(transcriptPath, agentID)
	if err != nil {
		return 0, err
	}
	return sumClaudeUsage(ctx, data)
}

func claudeTranscriptData(transcriptPath, agentID string) ([]byte, error) {
	dir, base := filepath.Dir(transcriptPath), filepath.Base(transcriptPath)
	if !filepath.IsAbs(transcriptPath) || filepath.Base(dir) != "subagents" ||
		len(agentID) > MaxCorrelationIDBytes || !claudeAgentIDShape.MatchString(agentID) ||
		base != "agent-"+agentID+".jsonl" {
		return nil, errors.New("invalid Claude subagent transcript path")
	}
	data, err := util.ReadConfinedLimited(filepath.Dir(dir), filepath.Join("subagents", base), maxClaudeTranscriptBytes)
	if err != nil {
		return nil, fmt.Errorf("read Claude subagent transcript: %w", err)
	}
	return data, nil
}

func sumClaudeUsage(ctx context.Context, data []byte) (int64, error) {
	if len(data) > maxClaudeTranscriptBytes || bytes.Count(data, []byte{'\n'}) > maxClaudeTranscriptLines {
		return 0, errors.New("claude subagent transcript exceeds read bounds")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(lines) > maxClaudeTranscriptLines {
		return 0, errors.New("claude subagent transcript exceeds line bound")
	}
	var total int64
	for index := 0; index < len(lines) && index < maxClaudeTranscriptLines; index++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := addClaudeUsage(&total, lines[index]); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// The harvester cannot be imported here: its compiler dependency imports agenthook.
// Reuse strictjson for validation and decode only this receipt's token projection.
func addClaudeUsage(total *int64, raw []byte) error {
	if err := strictjson.Validate(raw, strictjson.Options{MaxBytes: maxClaudeTranscriptLineBytes, MaxDepth: 64}); err != nil {
		return fmt.Errorf("validate Claude usage row: %w", err)
	}
	var record struct {
		Type    string `json:"type"`
		Message *struct {
			Usage map[string]json.RawMessage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("decode Claude usage row: %w", err)
	}
	if record.Type != "assistant" || record.Message == nil {
		return nil
	}
	return addClaudeTokens(total, record.Message.Usage)
}

func addClaudeTokens(total *int64, usage map[string]json.RawMessage) error {
	for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		var tokens int64
		if raw, found := usage[key]; found {
			if err := json.Unmarshal(raw, &tokens); err != nil {
				return fmt.Errorf("decode Claude token count: %w", err)
			}
		}
		if tokens < 0 || tokens > router.MaxOutcomeTokens-*total {
			return errors.New("claude subagent tokens exceed outcome bounds")
		}
		*total += tokens
	}
	return nil
}
