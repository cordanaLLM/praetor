package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranspiler_CompileAndBudget(t *testing.T) {
	tmpDir := t.TempDir()
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")

	sampleAgentsMD := `# Sample Agent Harness
Run verification:
` + "```bash\nmake verify-all\n```\n" + `
- Invariant 1: Recursion banned
- Invariant 2: Bounded loops
`
	if err := os.WriteFile(agentsPath, []byte(sampleAgentsMD), 0644); err != nil {
		t.Fatalf("failed to write test AGENTS.md: %v", err)
	}

	tr := NewTranspiler()
	res, err := tr.Compile(agentsPath)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}

	if len(res.Files) != 6 {
		t.Fatalf("expected 6 compiled files, got %d", len(res.Files))
	}

	for _, f := range res.Files {
		if f.LineCount > MaxLineBudget {
			t.Fatalf("file %s exceeded line budget: %d > %d", f.RelativePath, f.LineCount, MaxLineBudget)
		}
	}

	// Test write and verify
	if err := tr.WriteOutputs(res, tmpDir); err != nil {
		t.Fatalf("failed to write outputs: %v", err)
	}

	if err := tr.Verify(agentsPath, tmpDir); err != nil {
		t.Fatalf("expected verification to pass, got: %v", err)
	}
}
