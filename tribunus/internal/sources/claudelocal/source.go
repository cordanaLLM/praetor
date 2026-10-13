// Package claudelocal measures the Claude Code subscription's usage windows
// from the local session logs. It is read-only: it never writes to the
// projects directory.
//
// Session logs are JSON Lines under ~/.claude/projects/**/*.jsonl. Two line
// shapes matter (field names verified against the logs on kcromm,
// 2026-10-13; values not read):
//
//	{"type":"assistant","timestamp":"...","requestId":"...","sessionId":"...","agentId":"...",
//	 "message":{"id":"...","model":"claude-opus-5-5","usage":{"input_tokens":N,"output_tokens":N,
//	   "cache_creation_input_tokens":N,"cache_read_input_tokens":N,
//	   "cache_creation":{"ephemeral_5m_input_tokens":N,"ephemeral_1h_input_tokens":N},"speed":"..."}}}
//	{"type":"assistant","timestamp":"...","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1789858494}}
//
// Only these usage and quota fields are decoded. Message content, tool input
// and every other field is never decoded into a value, kept or logged.
//
// The provider reports no usage percentage in these logs, so the source
// derives one. The cost-equivalent of the usage inside the window is divided
// by a ceiling: the cost-equivalent the window held when the provider
// rejected a request. Without an observed rejection the ceiling is unknown,
// and the record carries no used_percent, which the router's window governor
// refuses as unknown.
package claudelocal

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
	"github.com/cordanaLLM/praetor/tribunus/internal/sources/sessionlog"
)

// SourceName identifies this source in Snapshot.SourceRuns and CLI flags.
const SourceName = "claude-local"

const (
	// maxEntriesWalked bounds the directory walk (HISS-02).
	maxEntriesWalked = 50000
	// maxFilesRead bounds the session files read in one fetch.
	maxFilesRead = 10000
	// maxUsageEvents bounds the usage entries kept across all files.
	maxUsageEvents = 1000000
	// maxRejections bounds the rejected-request entries kept.
	maxRejections = 10000
	// weeklyWindow is the longest window measured; files not written within
	// it cannot hold usage of any current window and are not read.
	weeklyWindow = 7 * 24 * time.Hour
	// burnWindow is the sliding window the burn and the active agents are measured over.
	burnWindow = 15 * time.Minute
)

// scanLimits bounds the scan of one session file. Lines longer than
// MaxLineBytes (large tool results) are skipped whole.
var scanLimits = sessionlog.Limits{MaxFileBytes: 256 << 20, MaxLines: 1000000, MaxLineBytes: 8 << 20}

// Result is one Fetch attempt's outcome.
type Result struct {
	Records []catalog.Record
	Status  catalog.Status
	Count   int
	Detail  string
}

// DefaultProjectsDir returns ~/.claude/projects. Callers pass their own
// directory to Fetch so tests never depend on $HOME.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude-local: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Fetch reads every session file under projectsDir written within the
// weekly window and returns one record per usage window: five_hour and
// seven_day always, plus every other rateLimitType a rejection names. A
// missing directory or one without session files is a skip.
func Fetch(ctx context.Context, projectsDir string, now time.Time) Result {
	logs := newLogScan(now)
	found, err := sessionlog.WalkJSONL(ctx, projectsDir, maxEntriesWalked, func(path string, info fs.FileInfo) error {
		return logs.readFile(ctx, path, info)
	})
	if err != nil {
		return Result{Status: catalog.StatusFail, Detail: "claude-local: " + err.Error()}
	}
	if !found {
		return Result{Status: catalog.StatusSkip, Detail: fmt.Sprintf("no *.jsonl session files under %s", projectsDir)}
	}
	records := logs.records()
	return Result{Records: records, Status: catalog.StatusOK, Count: len(records), Detail: logs.detail()}
}

// readFile scans one session file, unless it was last written before the weekly window.
func (s *logScan) readFile(ctx context.Context, path string, info fs.FileInfo) error {
	if info.ModTime().Before(s.now.Add(-weeklyWindow)) {
		return nil
	}
	s.files++
	if s.files > maxFilesRead {
		return fmt.Errorf("more than %d session files written within %s", maxFilesRead, weeklyWindow)
	}
	skipped, err := sessionlog.ScanLines(ctx, path, scanLimits, s.line)
	s.skippedLines += skipped
	if err != nil {
		return err
	}
	if s.overflow != "" {
		return fmt.Errorf("%s", s.overflow)
	}
	return nil
}

func (s *logScan) detail() string {
	return fmt.Sprintf("%d files read, %d usage entries, %d rejections, %d overlong lines skipped",
		s.files, len(s.usage), len(s.rejections), s.skippedLines)
}
