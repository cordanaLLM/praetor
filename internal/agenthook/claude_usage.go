package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

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
	var total int64
	err := forEachClaudeTranscriptLine(ctx, data, func(line []byte) error { return addClaudeUsage(&total, line) })
	if err != nil {
		return 0, err
	}
	return total, nil
}

// forEachClaudeTranscriptLine hands every line of a bounded subagent transcript to visit, in order,
// and stops at the first error or a cancelled context. Usage totals and claimed-run checks share it.
func forEachClaudeTranscriptLine(ctx context.Context, data []byte, visit func([]byte) error) error {
	if len(data) > maxClaudeTranscriptBytes || bytes.Count(data, []byte{'\n'}) > maxClaudeTranscriptLines {
		return errors.New("claude subagent transcript exceeds read bounds")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(lines) > maxClaudeTranscriptLines {
		return errors.New("claude subagent transcript exceeds line bound")
	}
	for index := 0; index < len(lines) && index < maxClaudeTranscriptLines; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(lines[index]); err != nil {
			return err
		}
	}
	return nil
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

// readClaudeBashCommands reads all executed Bash tool commands from the subagent transcript.
func readClaudeBashCommands(ctx context.Context, transcriptPath, agentID string) ([]string, error) {
	data, err := claudeTranscriptData(transcriptPath, agentID)
	if err != nil {
		return nil, err
	}
	return collectClaudeBashCommands(ctx, data)
}

func collectClaudeBashCommands(ctx context.Context, data []byte) ([]string, error) {
	var commands []string
	err := forEachClaudeTranscriptLine(ctx, data, func(line []byte) error { return extractClaudeBashLine(&commands, line) })
	if err != nil {
		return nil, err
	}
	return commands, nil
}

func extractClaudeBashLine(commands *[]string, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := strictjson.Validate(raw, strictjson.Options{MaxBytes: maxClaudeTranscriptLineBytes, MaxDepth: 64}); err != nil {
		return fmt.Errorf("validate Claude transcript row: %w", err)
	}
	var record struct {
		Type    string          `json:"type"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Message *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("decode Claude transcript row: %w", err)
	}
	if record.Type == "tool_use" && strings.EqualFold(record.Name, "bash") && len(record.Input) > 0 {
		return appendBashInput(commands, record.Input)
	}
	if record.Type == "assistant" && record.Message != nil && len(record.Message.Content) > 0 {
		return extractBashBlocks(commands, record.Message.Content)
	}
	return nil
}

func extractBashBlocks(commands *[]string, content json.RawMessage) error {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return fmt.Errorf("decode tool blocks: %w", err)
	}
	for i := 0; i < len(blocks) && i < 1024; i++ {
		b := blocks[i]
		if b.Type == "tool_use" && strings.EqualFold(b.Name, "bash") && len(b.Input) > 0 {
			if err := appendBashInput(commands, b.Input); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendBashInput(commands *[]string, rawInput json.RawMessage) error {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rawInput, &in); err != nil {
		return fmt.Errorf("decode bash command: %w", err)
	}
	if cmd := strings.TrimSpace(in.Command); cmd != "" {
		*commands = append(*commands, cmd)
	}
	return nil
}
