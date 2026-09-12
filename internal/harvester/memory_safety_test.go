package harvester

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestExtractMemoryInsightsRejectsEscapingAncestor(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "transcript.jsonl"), "HISS-01 private transcript\n")
	generated := filepath.Join(root, "conversation", ".system_generated")
	mustMkdirAll(t, generated)
	if err := os.Symlink(outside, filepath.Join(generated, "logs")); err != nil {
		t.Fatal(err)
	}
	insights, err := ExtractMemoryInsights(context.Background(), root)
	if !errors.Is(err, util.ErrPathEscapesRoot) || len(insights) != 0 {
		t.Fatalf("escaped transcript read: %+v %v", insights, err)
	}
}
