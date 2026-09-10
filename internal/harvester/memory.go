package harvester

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxTranscriptsScan = 50
	MaxLinesPerLog     = 2000
)

// MemoryInsight represents an extracted operational pattern or rule insight.
type MemoryInsight struct {
	Source    string    `json:"source"`
	Category  string    `json:"category"`
	Summary   string    `json:"summary"`
	Timestamp time.Time `json:"timestamp"`
}

// ExtractMemoryInsights scans conversation transcripts for recurring patterns and rules.
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
		return insights, nil
	}

	scanCount := 0
	for _, entry := range entries {
		if scanCount >= MaxTranscriptsScan {
			break
		}
		scanCount++

		if !entry.IsDir() {
			continue
		}

		transcriptPath := filepath.Join(transcriptsRoot, entry.Name(), ".system_generated", "logs", "transcript.jsonl")
		file, err := os.Open(transcriptPath)
		if err != nil {
			continue
		}

		processTranscript(file, entry.Name(), &insights)
		_ = file.Close()
	}

	return insights, nil
}

func processTranscript(file *os.File, convoID string, insights *[]MemoryInsight) {
	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		if lineCount >= MaxLinesPerLog {
			break
		}
		lineCount++

		text := scanner.Text()
		if strings.Contains(text, "HISS-") || strings.Contains(text, "invariant") {
			*insights = append(*insights, MemoryInsight{
				Source:    convoID,
				Category:  "governance-invariant",
				Summary:   "Detected explicit HISS invariant mention in transcript trajectory",
				Timestamp: time.Now(),
			})
			break
		}
	}
}
