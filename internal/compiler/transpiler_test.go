package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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

func TestTranspilerProductionBound(t *testing.T) {
	if DefaultAgentBound < contextopt.MaxDuration || DefaultAgentBound <= 10*time.Second {
		t.Fatalf("DefaultAgentBound = %s; want at least contextopt.MaxDuration (%s) above 10s", DefaultAgentBound, contextopt.MaxDuration)
	}
	if got := NewTranspiler().Bound; got != DefaultAgentBound {
		t.Fatalf("NewTranspiler().Bound = %s; want %s", got, DefaultAgentBound)
	}
}

func compileSampleFixture(t *testing.T) (string, string, *CompileResult) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Policy\n")
	res, err := NewTranspiler().Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	return dir, source, res
}

func TestTranspilerWriteOutputsSlowWithinBound(t *testing.T) {
	// Scale the production bound and a 12-second target write by 100. The old
	// 10-second budget (100ms scaled) expires on .windsurfrules; the publish-sized
	// budget completes and verifies.
	dir, source, res := compileSampleFixture(t)
	tr := NewTranspiler()
	tr.Bound = DefaultAgentBound / 100
	tr.writeTarget = func(ctx context.Context, root, rel string, data []byte) error {
		if rel == ".windsurfrules" {
			timer := time.NewTimer(120 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return writeConfinedText(ctx, root, rel, data)
	}
	if err := tr.WriteOutputs(res, dir); err != nil {
		t.Fatalf("scaled 12s target write exceeded bound %s: %v", tr.Bound, err)
	}
	if err := tr.Verify(source, dir); err != nil {
		t.Fatalf("verify written outputs: %v", err)
	}
}

func TestTranspilerWriteOutputsDeadlineNamesBoundAndTarget(t *testing.T) {
	const bound = 80 * time.Millisecond
	for _, lateSuccess := range []bool{false, true} {
		dir, _, res := compileSampleFixture(t)
		tr := NewTranspiler()
		tr.Bound = bound
		tr.writeTarget = func(ctx context.Context, root, rel string, data []byte) error {
			if rel != ".windsurfrules" {
				return writeConfinedText(ctx, root, rel, data)
			}
			<-ctx.Done()
			if lateSuccess {
				return nil
			}
			return ctx.Err()
		}
		err := tr.WriteOutputs(res, dir)
		assertTargetDeadline(t, err, bound, ".windsurfrules")
	}
}

func TestTranspilerVerifyDeadlineNamesBoundAndTarget(t *testing.T) {
	const bound = 80 * time.Millisecond
	for _, lateSuccess := range []bool{false, true} {
		dir, source, res := compileSampleFixture(t)
		if err := NewTranspiler().WriteOutputs(res, dir); err != nil {
			t.Fatal(err)
		}
		tr := NewTranspiler()
		tr.Bound = bound
		tr.readTarget = func(ctx context.Context, root, rel string) ([]byte, error) {
			if rel != ".codex/rules.md" {
				return readConfinedText(ctx, root, rel)
			}
			<-ctx.Done()
			if lateSuccess {
				return []byte("drifted\n"), nil
			}
			return nil, ctx.Err()
		}
		err := tr.Verify(source, dir)
		assertTargetDeadline(t, err, bound, ".codex/rules.md")
	}
}

func assertTargetDeadline(t *testing.T, err error, bound time.Duration, target string) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want wrapped context.DeadlineExceeded, got %v", err)
	}
	for _, want := range []string{"target " + target, bound.String(), "bound"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err.Error(), want)
		}
	}
	for _, avoid := range []string{"out of sync", "missing or unreadable"} {
		if strings.Contains(err.Error(), avoid) {
			t.Fatalf("deadline error misclassified as %q: %v", avoid, err)
		}
	}
}

func TestTranspilerVerifyMismatchDistinctFromDeadline(t *testing.T) {
	dir, source, res := compileSampleFixture(t)
	tr := NewTranspiler()
	if err := tr.WriteOutputs(res, dir); err != nil {
		t.Fatal(err)
	}
	writeCanonicalFixture(t, filepath.Join(dir, ".codex", "rules.md"), "tampered\n")
	err := tr.Verify(source, dir)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want content mismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "target .codex/rules.md is out of sync") || strings.Contains(err.Error(), "bound") {
		t.Fatalf("mismatch error = %v; want out-of-sync without bound", err)
	}
}

func TestTranspilerCompileDeadlineNamesBoundAndSource(t *testing.T) {
	_, source, _ := compileSampleFixture(t)
	tr := NewTranspiler()
	tr.Bound = 80 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	_, err := tr.CompileContext(ctx, source)
	assertTargetDeadline(t, err, tr.Bound, source)
}
