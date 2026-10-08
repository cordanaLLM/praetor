// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
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

// usageSnapshot is the largest usage seen so far for one response (message id plus request id).
// A response is written as one line per content block and every line repeats the usage, so
// later lines can only raise the counts.
type usageSnapshot struct {
	input, cacheCreation, cacheRead, output int64
	frontier                                bool
}

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
	seenResponses       map[string]usageSnapshot
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

// isOperatorTouch reports a human prompt: a user record whose origin is human and that is
// neither meta, a compact summary nor part of a sidechain. Task notifications, peer
// messages, hook output and tool results carry another origin or none.
func isOperatorTouch(rec *harvester.LedgerRecord) bool {
	if rec.Type != "user" || rec.IsMeta || rec.IsCompactSummary || rec.IsSidechain {
		return false
	}
	return rec.Origin != nil && rec.Origin.Kind == "human"
}

func responseKey(rec *harvester.LedgerRecord) string {
	if rec.Message == nil || rec.Message.ID == "" {
		return ""
	}
	return rec.Message.ID + ":" + rec.RequestID
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

// applyUsage counts a response once. A repeated key only adds the growth of its usage.
func (s *BranchTranscriptStats) applyUsage(rec *harvester.LedgerRecord, classifier *Classifier) {
	usage := rec.Message.Usage
	key := responseKey(rec)
	prev, repeated := s.seenResponses[key]
	if key == "" {
		prev, repeated = usageSnapshot{}, false
	}
	next := usageSnapshot{
		input:         maxInt64(prev.input, usage.InputTokens),
		cacheCreation: maxInt64(prev.cacheCreation, usage.CacheCreationInputTokens),
		cacheRead:     maxInt64(prev.cacheRead, usage.CacheReadInputTokens),
		output:        maxInt64(prev.output, usage.OutputTokens),
		frontier:      prev.frontier,
	}
	if !repeated {
		s.TotalRequests++
		next.frontier = classifier.IsFrontier("", rec.Message.Model)
		if classifier.IsLocal("", rec.Message.Model) {
			s.LocalRequests++
		}
	}
	s.InputTokens += next.input - prev.input
	s.CacheCreationTokens += next.cacheCreation - prev.cacheCreation
	s.CacheReadTokens += next.cacheRead - prev.cacheRead
	s.OutputTokens += next.output - prev.output
	if next.frontier {
		s.FrontierTokens += (next.input + next.cacheCreation + next.cacheRead + next.output) -
			(prev.input + prev.cacheCreation + prev.cacheRead + prev.output)
	}
	if key != "" {
		s.seenResponses[key] = next
	}
}

func (s *BranchTranscriptStats) apply(rec *harvester.LedgerRecord, classifier *Classifier) {
	if isOperatorTouch(rec) {
		s.OperatorTouches++
	}
	if rec.Message != nil && rec.Message.Usage != nil {
		s.applyUsage(rec, classifier)
	}
}

// SourceNotes lists what a read skipped. Undecodable lines are reported with file and line,
// never dropped; limit overflows and I/O errors fail the read instead.
type SourceNotes struct {
	SkippedLines int
	Examples     []string
}

const maxNoteExamples = 10

func (n *SourceNotes) skip(source string, line int, err error) {
	n.SkippedLines++
	if len(n.Examples) < maxNoteExamples {
		n.Examples = append(n.Examples, fmt.Sprintf("%s:%d: %v", source, line, err))
	}
}

// Lines describes the notes as report lines.
func (n *SourceNotes) Lines() []string {
	if n == nil || n.SkippedLines == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("skipped %d undecodable transcript lines (first %d listed)", n.SkippedLines, len(n.Examples))}
	return append(out, n.Examples...)
}

// sourceLimits bounds one source read (HISS-02). Exceeding any bound is an error.
type sourceLimits struct {
	Files int
	Lines int
	Bytes int64
}

func defaultLimits() sourceLimits {
	return sourceLimits{Files: MaxSourceFiles, Lines: MaxSourceLines, Bytes: MaxFileBytes}
}

type transcriptReader struct {
	classifier *Classifier
	byBranch   map[string]*BranchTranscriptStats
	notes      *SourceNotes
	limits     sourceLimits
}

func (r *transcriptReader) line(source string, lineNo int, raw []byte) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	rec, err := harvester.DecodeLedgerLine(raw)
	if err != nil {
		r.notes.skip(source, lineNo, err)
		return nil
	}
	branch := strings.TrimSpace(rec.GitBranch)
	if branch == "" {
		return nil
	}
	stats, ok := r.byBranch[branch]
	if !ok {
		stats = &BranchTranscriptStats{seenResponses: make(map[string]usageSnapshot)}
		r.byBranch[branch] = stats
	}
	stats.apply(rec, r.classifier)
	return nil
}

func (r *transcriptReader) stream(ctx context.Context, source string, in io.Reader) error {
	limits := harvester.ScanLimits{MaxRecords: r.limits.Lines, MaxBytes: r.limits.Bytes}
	err := harvester.ScanTranscriptLines(ctx, in, limits, func(lineNo int, raw []byte) error {
		return r.line(source, lineNo, raw)
	})
	if err != nil {
		return fmt.Errorf("transcript %s: %w", source, err)
	}
	return nil
}

func (r *transcriptReader) file(ctx context.Context, path string) (resultErr error) {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- path is configured in the manifest, a flag or a bounded directory walk.
	if err != nil {
		return fmt.Errorf("open transcript file %s: %w", path, err)
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	return r.stream(ctx, path, f)
}

type transcriptWalker struct {
	reader    *transcriptReader
	cleanDir  string
	fileCount int
}

func (w *transcriptWalker) walk(ctx context.Context) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() != filepath.Base(w.cleanDir) && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		w.fileCount++
		if w.fileCount > w.reader.limits.Files {
			return fmt.Errorf("transcripts directory %s exceeds %d files limit", w.cleanDir, w.reader.limits.Files)
		}
		return w.reader.file(ctx, path)
	}
}

// ReadTranscriptStream reads one session stream; the source label names it in notes and errors.
func ReadTranscriptStream(ctx context.Context, source string, in io.Reader, classifier *Classifier, byBranch map[string]*BranchTranscriptStats) (*SourceNotes, error) {
	reader := &transcriptReader{classifier: classifier, byBranch: byBranch, notes: &SourceNotes{}, limits: defaultLimits()}
	return reader.notes, reader.stream(ctx, source, in)
}

// ReadTranscriptsDir reads all .jsonl files in dir, subagent sessions included, bounded by
// the source limits. Overflow of the file, line or byte bound is an error.
func ReadTranscriptsDir(ctx context.Context, dir string, classifier *Classifier) (map[string]*BranchTranscriptStats, *SourceNotes, error) {
	return readTranscriptsDir(ctx, dir, classifier, defaultLimits())
}

func readTranscriptsDir(ctx context.Context, dir string, classifier *Classifier, limits sourceLimits) (map[string]*BranchTranscriptStats, *SourceNotes, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" {
		return nil, nil, errors.New("empty transcripts directory")
	}
	if _, err := os.Stat(cleanDir); err != nil {
		return nil, nil, fmt.Errorf("read transcripts directory %s: %w", cleanDir, err)
	}
	reader := &transcriptReader{classifier: classifier, byBranch: make(map[string]*BranchTranscriptStats), notes: &SourceNotes{}, limits: limits}
	walker := &transcriptWalker{reader: reader, cleanDir: cleanDir}
	if err := filepath.WalkDir(cleanDir, walker.walk(ctx)); err != nil {
		return nil, nil, fmt.Errorf("walk transcripts in %s: %w", cleanDir, err)
	}
	return reader.byBranch, reader.notes, nil
}
