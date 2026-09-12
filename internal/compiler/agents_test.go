package compiler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompileAgents_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	srcDir := filepath.Join(tmpDir, ".agents", "agents")
	outDir := filepath.Join(tmpDir, "out")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src dir: %v", err)
	}

	testAgent := filepath.Join(srcDir, "test-helper.md")
	content := "---\nname: test-helper\n---\n# Test Helper"
	if err := os.WriteFile(testAgent, []byte(content), 0644); err != nil {
		t.Fatalf("write test agent: %v", err)
	}

	files, err := CompileAgents(ctx, srcDir, outDir)
	if err != nil {
		t.Fatalf("CompileAgents failed: %v", err)
	}

	if len(files) != 4 {
		t.Fatalf("expected 4 vendor projections, got %d", len(files))
	}

	for _, expectedVendor := range []string{".claude", ".codex", ".github", ".gemini"} {
		expectedFile := filepath.Join(outDir, expectedVendor, "agents", "test-helper.md")
		if _, statErr := os.Stat(expectedFile); os.IsNotExist(statErr) {
			t.Errorf("expected vendor file %s to exist", expectedFile)
		}
	}
}

func TestCompileAgents_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmpDir := t.TempDir()
	_, err := CompileAgents(ctx, tmpDir, tmpDir)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}

	var absentContext context.Context
	_, nilErr := CompileAgents(absentContext, tmpDir, tmpDir)
	if nilErr == nil {
		t.Fatal("expected error for nil context, got nil")
	}
}

func TestCompileAgents_Boundary_EmptyAndNonExistent(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Non-existent directory returns nil without error
	files, err := CompileAgents(ctx, filepath.Join(tmpDir, "nonexistent"), tmpDir)
	if err != nil {
		t.Fatalf("expected no error on non-existent dir, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}

	// Directory with non-markdown file
	emptyDir := filepath.Join(tmpDir, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyDir, "ignore.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err = CompileAgents(ctx, emptyDir, tmpDir)
	if err != nil {
		t.Fatalf("expected no error on empty dir, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}
