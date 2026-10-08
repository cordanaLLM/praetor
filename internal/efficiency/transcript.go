// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BranchTranscriptStats aggregates metrics from joined transcript session lines for a branch.
type BranchTranscriptStats struct {
	OperatorTouches     int
	FrontierTokens      int64
	InputTokens         int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	OutputTokens        int64
	LocalRequests       int
	TotalRequests       int
}

// PromptCacheHitRate returns cache_read / (input + cache_creation + cache_read).
func (s BranchTranscriptStats) PromptCacheHitRate() (float64, bool) {
	denom := s.InputTokens + s.CacheCreationTokens + s.CacheReadTokens
	if denom <= 0 {
		return 0, false
	}
	return float64(s.CacheReadTokens) / float64(denom), true
}

// LocalFirstRatio returns local_requests / total_requests.
func (s BranchTranscriptStats) LocalFirstRatio() (float64, bool) {
	if s.TotalRequests <= 0 {
		return 0, false
	}
	return float64(s.LocalRequests) / float64(s.TotalRequests), true
}

type rawTranscriptLine struct {
	GitBranch string      `json:"gitBranch"`
	Type      string      `json:"type"`
	Role      string      `json:"role"`
	Source    string      `json:"source"`
	Model     string      `json:"model"`
	HookName  string      `json:"hookName,omitempty"`
	HookEvent string      `json:"hookEvent,omitempty"`
	ToolUseID string      `json:"toolUseID,omitempty"`
	Origin    *rawOrigin  `json:"origin,omitempty"`
	Message   *rawMessage `json:"message,omitempty"`
	Usage     *rawUsage   `json:"usage,omitempty"`
	RawBytes  []byte      `json:"-"`
}

type rawOrigin struct {
	Kind string `json:"kind"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Type    string          `json:"type"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

func isHookOrSystem(line *rawTranscriptLine) bool {
	if line.Type == "attachment" || line.HookName != "" || line.HookEvent != "" {
		return true
	}
	if strings.EqualFold(line.Source, "system") {
		return true
	}
	if line.Origin != nil {
		k := strings.ToLower(line.Origin.Kind)
		if k == "hook" || k == "system" {
			return true
		}
	}
	return false
}

func isToolResult(line *rawTranscriptLine) bool {
	if line.ToolUseID != "" || strings.EqualFold(line.Type, "tool_result") {
		return true
	}
	if line.Message != nil && len(line.Message.Content) > 0 {
		contentStr := string(line.Message.Content)
		if strings.Contains(contentStr, `"tool_result"`) || strings.Contains(contentStr, `"tool_use_id"`) {
			return true
		}
	}
	return false
}

func isOperatorTouch(line *rawTranscriptLine) bool {
	if isHookOrSystem(line) || isToolResult(line) {
		return false
	}
	if strings.EqualFold(line.Type, "user") || strings.EqualFold(line.Type, "user_input") ||
		strings.EqualFold(line.Source, "user_explicit") {
		return true
	}
	if line.Message != nil && (strings.EqualFold(line.Message.Role, "user") || strings.EqualFold(line.Message.Type, "user")) {
		return true
	}
	return false
}

func extractUsageAndModel(line *rawTranscriptLine) (*rawUsage, string) {
	var usage *rawUsage
	var model string
	if line.Usage != nil {
		usage = line.Usage
	} else if line.Message != nil && line.Message.Usage != nil {
		usage = line.Message.Usage
	}
	if line.Model != "" {
		model = line.Model
	} else if line.Message != nil && line.Message.Model != "" {
		model = line.Message.Model
	}
	return usage, model
}

func applyTranscriptLine(line *rawTranscriptLine, classifier *Classifier, stats *BranchTranscriptStats) {
	if isOperatorTouch(line) {
		stats.OperatorTouches++
	}
	usage, model := extractUsageAndModel(line)
	if usage == nil {
		return
	}
	stats.TotalRequests++
	if classifier != nil && classifier.IsLocal(model) {
		stats.LocalRequests++
	}
	lineTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens + usage.OutputTokens
	if classifier != nil && classifier.IsFrontier(model) {
		stats.FrontierTokens += lineTokens
	}
	stats.InputTokens += usage.InputTokens
	stats.CacheCreationTokens += usage.CacheCreationInputTokens
	stats.CacheReadTokens += usage.CacheReadInputTokens
	stats.OutputTokens += usage.OutputTokens
}

// ReadTranscriptStream reads and decodes JSON lines from a stream bounded by MaxSourceLines.
func ReadTranscriptStream(ctx context.Context, r io.Reader, classifier *Classifier, byBranch map[string]*BranchTranscriptStats) error {
	scanner := bufio.NewScanner(io.LimitReader(r, MaxFileBytes))
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, MaxFileBytes)

	for lineCount := 0; lineCount < MaxSourceLines && scanner.Scan(); lineCount++ {
		if lineCount%1000 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		lineBytes := bytes.TrimSpace(scanner.Bytes())
		if len(lineBytes) == 0 {
			continue
		}
		var raw rawTranscriptLine
		if err := json.Unmarshal(lineBytes, &raw); err != nil {
			continue
		}
		branch := strings.TrimSpace(raw.GitBranch)
		if branch == "" {
			continue
		}
		stats, ok := byBranch[branch]
		if !ok {
			stats = &BranchTranscriptStats{}
			byBranch[branch] = stats
		}
		applyTranscriptLine(&raw, classifier, stats)
	}
	return scanner.Err()
}

// ReadTranscriptFile reads a single transcript JSONL file.
func ReadTranscriptFile(ctx context.Context, path string, classifier *Classifier, byBranch map[string]*BranchTranscriptStats) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanPath := filepath.Clean(path)
	f, err := os.Open(cleanPath) // #nosec G304 -- path is configured in manifest or resolved from configured directory.
	if err != nil {
		return fmt.Errorf("open transcript file %s: %w", cleanPath, err)
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	return ReadTranscriptStream(ctx, f, classifier, byBranch)
}

// ReadTranscriptsDir reads all .jsonl files in dir bounded by MaxSourceFiles.
func ReadTranscriptsDir(ctx context.Context, dir string, classifier *Classifier) (map[string]*BranchTranscriptStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" {
		return nil, errors.New("empty transcripts directory")
	}
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("read transcripts directory %s: %w", cleanDir, err)
	}
	byBranch := make(map[string]*BranchTranscriptStats)
	fileCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		fileCount++
		if fileCount > MaxSourceFiles {
			break
		}
		filePath := filepath.Join(cleanDir, entry.Name())
		if err := ReadTranscriptFile(ctx, filePath, classifier, byBranch); err != nil {
			return nil, err
		}
	}
	return byBranch, nil
}
