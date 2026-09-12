package harvester

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxTranscriptsScan bounds how many conversation directories are inspected.
	MaxTranscriptsScan = 50
	// MaxLinesPerLog bounds how many lines of a single transcript are read.
	MaxLinesPerLog = 2000
	// transcriptScanBuffer is the initial bufio.Scanner buffer for a transcript line.
	transcriptScanBuffer = 64 * 1024
	// MaxTranscriptLineBytes is the largest single JSONL line accepted. Agent transcripts
	// routinely embed tool output well past bufio's 64 KiB default, and silently stopping
	// at the first oversized line would under-report every insight behind it.
	MaxTranscriptLineBytes = 8 * 1024 * 1024
)

// MemoryInsight represents an extracted operational pattern or rule insight.
type MemoryInsight struct {
	Source    string    `json:"source"`
	Category  string    `json:"category"`
	Summary   string    `json:"summary"`
	Timestamp time.Time `json:"timestamp"`
}

// ExtractMemoryInsights scans conversation transcripts for recurring patterns and rules.
// An absent transcripts root yields no insights; any other directory failure is reported so
// that a mistyped path can never be mistaken for an empty brain directory.
func ExtractMemoryInsights(ctx context.Context, transcriptsRoot string) ([]MemoryInsight, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before memory extraction: %w", err)
	}

	insights := make([]MemoryInsight, 0)
	if transcriptsRoot == "" {
		return insights, nil
	}

	entries, err := os.ReadDir(transcriptsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return insights, nil
		}
		return nil, fmt.Errorf("read transcripts root %s: %w", transcriptsRoot, err)
	}

	for i := 0; i < len(entries) && i < MaxTranscriptsScan; i++ {
		if cErr := ctx.Err(); cErr != nil {
			return nil, fmt.Errorf("context cancelled during memory extraction: %w", cErr)
		}
		if !entries[i].IsDir() {
			continue
		}
		transcriptPath, err := util.ConfinePath(transcriptsRoot, filepath.Join(entries[i].Name(), ".system_generated", "logs", "transcript.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("confine transcript source: %w", err)
		}
		if scanErr := scanTranscript(ctx, transcriptPath, entries[i].Name(), &insights); scanErr != nil {
			return nil, scanErr
		}
	}

	return insights, nil
}

// scanTranscript opens one transcript and appends the insights it yields. A missing
// transcript is normal; a read failure is propagated.
func scanTranscript(ctx context.Context, transcriptPath, convoID string, insights *[]MemoryInsight) (err error) {
	file, openErr := openRegularSource(transcriptPath)
	if openErr != nil {
		if errors.Is(openErr, os.ErrNotExist) || errors.Is(openErr, ErrNotRegularFile) {
			return nil
		}
		return fmt.Errorf("open transcript %s: %w", transcriptPath, openErr)
	}
	defer func() {
		if cErr := file.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close transcript %s: %w", transcriptPath, cErr)
		}
	}()

	if scanErr := processTranscript(ctx, file, convoID, insights); scanErr != nil {
		return fmt.Errorf("scan transcript %s: %w", transcriptPath, scanErr)
	}
	return nil
}

// processTranscript appends one governance insight when the transcript mentions a HISS
// invariant. A scanner failure (including a line above MaxTranscriptLineBytes) is returned
// rather than silently truncating the scan.
func processTranscript(ctx context.Context, file *os.File, convoID string, insights *[]MemoryInsight) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, transcriptScanBuffer), MaxTranscriptLineBytes)

	for lines := 0; lines < MaxLinesPerLog && scanner.Scan(); lines++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled while reading transcript: %w", err)
		}

		text := scanner.Text()
		if strings.Contains(text, "HISS-") || strings.Contains(text, "invariant") {
			*insights = append(*insights, MemoryInsight{
				Source:    convoID,
				Category:  "governance-invariant",
				Summary:   "Detected explicit HISS invariant mention in transcript trajectory",
				Timestamp: time.Now(),
			})
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read transcript lines: %w", err)
	}
	return nil
}
