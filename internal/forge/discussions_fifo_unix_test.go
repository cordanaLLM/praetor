//go:build unix

package forge

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTranscribeDiscussionToADR_Boundary_FIFOEntry asserts that a named pipe present in the ADR directory
// is skipped during content inspection (preventing an indefinite hang on open) and counted toward sequence numbering.
func TestTranscribeDiscussionToADR_Boundary_FIFOEntry(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	pipePath := filepath.Join(tempDir, "0001-pipe.md")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	disc := Discussion{
		ID:           101,
		Title:        "Decision After FIFO",
		Status:       "approved",
		ContextText:  "Testing FIFO entry handling",
		DecisionText: "TranscribeDiscussionToADR skips FIFO without hanging",
	}

	type result struct {
		adr *ADR
		err error
	}
	done := make(chan result, 1)
	go func() {
		adr, err := TranscribeDiscussionToADR(ctx, disc, tempDir, tempDir)
		done <- result{adr: adr, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error transcribing discussion with FIFO present: %v", res.err)
		}
		if res.adr.Number != 2 {
			t.Fatalf("expected next ADR number 2 (FIFO counted toward sequence), got %d", res.adr.Number)
		}
		if !strings.HasSuffix(res.adr.FilePath, "0002-decision-after-fifo.md") {
			t.Fatalf("unexpected file path: %s", res.adr.FilePath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TranscribeDiscussionToADR hung on FIFO entry")
	}
}
