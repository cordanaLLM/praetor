// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExtractMemoryInsightsLineBoundary(t *testing.T) {
	for _, total := range []int{MaxLinesPerLog - 1, MaxLinesPerLog, MaxLinesPerLog + 1} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			root := t.TempDir()
			logDir := filepath.Join(root, "conversation", ".system_generated", "logs")
			var builder strings.Builder
			for line := 1; line <= total; line++ {
				if line == total {
					builder.WriteString("HISS-02 final marker\n")
				} else {
					builder.WriteString("ordinary\n")
				}
			}
			mustWriteFile(t, filepath.Join(logDir, "transcript.jsonl"), builder.String())

			insights, err := ExtractMemoryInsights(context.Background(), root)
			if total <= MaxLinesPerLog {
				if err != nil || len(insights) != 1 {
					t.Fatalf("exact-bound transcript: insights=%d err=%v", len(insights), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "exceeds") || len(insights) != 0 {
				t.Fatalf("over-bound transcript: insights=%d err=%v", len(insights), err)
			}
		})
	}
}

func TestExtractMemoryInsightsPreservesPartialResultOnTruncation(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "conversation", ".system_generated", "logs")
	var builder strings.Builder
	builder.WriteString("HISS-01 early marker\n")
	for line := 2; line <= MaxLinesPerLog+1; line++ {
		builder.WriteString("ordinary\n")
	}
	mustWriteFile(t, filepath.Join(logDir, "transcript.jsonl"), builder.String())

	insights, err := ExtractMemoryInsights(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "exceeds") || len(insights) != 1 {
		t.Fatalf("partial result was not preserved: insights=%d err=%v", len(insights), err)
	}
}

func TestExtractMemoryInsightsReportsScannerError(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "conversation", ".system_generated", "logs")
	line := strings.Repeat("x", MaxTranscriptLineBytes+1)
	mustWriteFile(t, filepath.Join(logDir, "transcript.jsonl"), line+"\n")

	insights, err := ExtractMemoryInsights(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "token too long") || len(insights) != 0 {
		t.Fatalf("scanner error was not reported: insights=%d err=%v", len(insights), err)
	}
}
