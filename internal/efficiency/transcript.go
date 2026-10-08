// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/harvester"
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
	seenResponses       map[string]bool
}

func (s *BranchTranscriptStats) initSeen() {
	if s.seenResponses == nil {
		s.seenResponses = make(map[string]bool)
	}
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

func isHookOrSystem(rec *harvester.ClaudeRecord) bool {
	if rec.Type == "attachment" || rec.HookName != "" || rec.HookEvent != "" {
		return true
	}
	if strings.EqualFold(rec.Source, "system") {
		return true
	}
	if rec.Origin != nil {
		k := strings.ToLower(rec.Origin.Kind)
		if k == "hook" || k == "system" {
			return true
		}
	}
	return false
}

func isToolResult(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) bool {
	if rec.ToolUseID != "" || strings.EqualFold(rec.Type, "tool_result") {
		return true
	}
	if inner != nil {
		if strings.EqualFold(inner.Type, "tool_result") {
			return true
		}
		if len(inner.Content) > 0 {
			contentStr := string(inner.Content)
			if strings.Contains(contentStr, `"tool_result"`) || strings.Contains(contentStr, `"tool_use_id"`) {
				return true
			}
		}
	}
	return false
}

func isMetaOrSynthetic(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) bool {
	if rec.IsMeta || rec.IsCompactSummary || rec.IsSidechain {
		return true
	}
	return inner != nil && (inner.IsMeta || inner.IsCompactSummary || inner.IsSidechain)
}

func isUserRoleOrType(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) bool {
	if strings.EqualFold(rec.Type, "user") || strings.EqualFold(rec.Type, "user_input") || strings.EqualFold(rec.Source, "user_explicit") {
		return true
	}
	if inner != nil {
		return strings.EqualFold(inner.Role, "user") || strings.EqualFold(inner.Type, "user")
	}
	return false
}

func isOperatorTouch(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) bool {
	if isMetaOrSynthetic(rec, inner) {
		return false
	}
	if isHookOrSystem(rec) || isToolResult(rec, inner) {
		return false
	}
	if rec.Origin != nil && !strings.EqualFold(rec.Origin.Kind, "human") {
		return false
	}
	return isUserRoleOrType(rec, inner)
}

func extractUsageAndModel(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) (*harvester.ClaudeUsage, string) {
	var usage *harvester.ClaudeUsage
	var model string
	if rec.Usage != nil {
		usage = rec.Usage
	} else if inner != nil && inner.Usage != nil {
		usage = inner.Usage
	}
	if rec.Model != "" {
		model = rec.Model
	} else if inner != nil && inner.Model != "" {
		model = inner.Model
	}
	return usage, model
}

func dedupeKey(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage) string {
	msgID := rec.MessageID
	if inner != nil && inner.ID != "" {
		msgID = inner.ID
	}
	reqID := rec.RequestID
	if msgID != "" && reqID != "" {
		return msgID + ":" + reqID
	}
	if msgID != "" {
		return msgID
	}
	return reqID
}

func applyTranscriptLine(rec *harvester.ClaudeRecord, inner *harvester.ClaudeInnerMessage, classifier *Classifier, stats *BranchTranscriptStats) {
	stats.initSeen()
	if isOperatorTouch(rec, inner) {
		stats.OperatorTouches++
	}
	usage, model := extractUsageAndModel(rec, inner)
	if usage == nil {
		return
	}
	key := dedupeKey(rec, inner)
	if key != "" && stats.seenResponses[key] {
		return
	}
	if key != "" {
		stats.seenResponses[key] = true
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

type countReader struct {
	r     io.Reader
	count int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.count += int64(n)
	return n, err
}

// ReadTranscriptStream reads and decodes JSON lines from a stream bounded by MaxSourceLines.
func processTranscriptLine(lineBytes []byte, lineCount int, classifier *Classifier, byBranch map[string]*BranchTranscriptStats) error {
	rec, inner, err := harvester.DecodeClaudeLine(lineBytes)
	if err != nil {
		return fmt.Errorf("line %d: %w", lineCount+1, err)
	}
	branch := strings.TrimSpace(rec.GitBranch)
	if branch == "" {
		return nil
	}
	stats, ok := byBranch[branch]
	if !ok {
		stats = &BranchTranscriptStats{}
		byBranch[branch] = stats
	}
	applyTranscriptLine(rec, inner, classifier, stats)
	return nil
}

// ReadTranscriptStream reads and decodes JSON lines from a stream bounded by MaxSourceLines.
func ReadTranscriptStream(ctx context.Context, r io.Reader, classifier *Classifier, byBranch map[string]*BranchTranscriptStats) error {
	limitedReader := &countReader{r: io.LimitReader(r, MaxFileBytes+1)}
	scanner := bufio.NewScanner(limitedReader)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineCount := 0
	for ; lineCount < MaxSourceLines && scanner.Scan(); lineCount++ {
		if lineCount%1000 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		lineBytes := bytes.TrimSpace(scanner.Bytes())
		if len(lineBytes) == 0 {
			continue
		}
		if err := processTranscriptLine(lineBytes, lineCount, classifier, byBranch); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if limitedReader.count > MaxFileBytes {
		return fmt.Errorf("transcript exceeds %d byte limit", MaxFileBytes)
	}
	if scanner.Scan() {
		return fmt.Errorf("transcript exceeds %d lines limit", MaxSourceLines)
	}
	return nil
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
	info, err := f.Stat()
	if err == nil && info.Size() > MaxFileBytes {
		return fmt.Errorf("transcript file %s exceeds %d byte limit", cleanPath, MaxFileBytes)
	}
	return ReadTranscriptStream(ctx, f, classifier, byBranch)
}

func shouldSkipTranscriptDir(cleanDir, dirName string) bool {
	return dirName != filepath.Base(cleanDir) && strings.HasPrefix(dirName, ".")
}

type transcriptDirWalker struct {
	ctx        context.Context
	cleanDir   string
	classifier *Classifier
	byBranch   map[string]*BranchTranscriptStats
	fileCount  int
}

func (w *transcriptDirWalker) walk(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if d.IsDir() {
		if shouldSkipTranscriptDir(w.cleanDir, d.Name()) {
			return filepath.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(d.Name(), ".jsonl") {
		return nil
	}
	w.fileCount++
	if w.fileCount > MaxSourceFiles {
		return fmt.Errorf("transcripts directory %s exceeds %d files limit", w.cleanDir, MaxSourceFiles)
	}
	return ReadTranscriptFile(w.ctx, path, w.classifier, w.byBranch)
}

// ReadTranscriptsDir reads all .jsonl files in dir (including subdirectories) bounded by MaxSourceFiles.
func ReadTranscriptsDir(ctx context.Context, dir string, classifier *Classifier) (map[string]*BranchTranscriptStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" {
		return nil, errors.New("empty transcripts directory")
	}
	if _, err := os.Stat(cleanDir); err != nil {
		return nil, fmt.Errorf("read transcripts directory %s: %w", cleanDir, err)
	}
	walker := &transcriptDirWalker{
		ctx:        ctx,
		cleanDir:   cleanDir,
		classifier: classifier,
		byBranch:   make(map[string]*BranchTranscriptStats),
	}
	if err := filepath.WalkDir(cleanDir, walker.walk); err != nil {
		return nil, fmt.Errorf("walk transcripts in %s: %w", cleanDir, err)
	}
	return walker.byBranch, nil
}
