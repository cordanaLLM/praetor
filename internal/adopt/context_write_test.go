package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const contextWriteAgents = "# Repo rules\n- tests before commit\n- small commits\n"

// compileAgentContextIn runs CompileAgentContext over dir/AGENTS.md into dir.
func compileAgentContextIn(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := CompileAgentContext(t.Context(), &out, compiler.NewTranspiler(), filepath.Join(dir, agentsFile), dir)
	return out.String(), err
}

// Positive: in a work tree without an ignore rule the write merges the managed block, reports
// it, then compiles, and the result passes compile-context --verify.
func TestCompileAgentContext_Positive(t *testing.T) {
	root := newTestRepo(t, "context-write")
	mustWrite(t, filepath.Join(root, agentsFile), contextWriteAgents)
	out, err := compileAgentContextIn(t, root)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	for _, want := range []string{"Added the Praetor private-artifact block to .gitignore", "completed successfully"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := mustRead(t, filepath.Join(root, ".gitignore")); got != ManagedGitIgnoreBlock() {
		t.Fatalf(".gitignore = %q; want the managed block", got)
	}
	var verify strings.Builder
	if err := compiler.VerifyCompiledContext(t.Context(), &verify, compiler.NewTranspiler(), filepath.Join(root, agentsFile), root); err != nil {
		t.Fatalf("verify after the write: %v\n%s", err, verify.String())
	}
}

// Negative: a refused ignore reconciliation compiles nothing: AGENTS.md keeps its text, no
// register block is spliced and no vendor file is written.
func TestCompileAgentContext_Negative(t *testing.T) {
	root := newTestRepo(t, "context-write-refused")
	mustWrite(t, filepath.Join(root, agentsFile), contextWriteAgents)
	unterminated := gitIgnoreManagedBegin + "\n/.workingdir2/\n"
	mustWrite(t, filepath.Join(root, ".gitignore"), unterminated)
	_, err := compileAgentContextIn(t, root)
	if err == nil || !strings.Contains(err.Error(), "compiled no agent context") || !strings.Contains(err.Error(), ".workingdir/evidence/") {
		t.Fatalf("want a refusal naming the evidence directory, got %v", err)
	}
	if got := mustRead(t, filepath.Join(root, agentsFile)); got != contextWriteAgents {
		t.Fatalf("a refused write spliced AGENTS.md: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused write compiled CLAUDE.md: %v", statErr)
	}
}

// Boundary: a directory in no Git work tree needs no ignore rule, so the write compiles
// without creating .gitignore or printing a notice.
func TestCompileAgentContext_Boundary(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, agentsFile), contextWriteAgents)
	out, err := compileAgentContextIn(t, dir)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if strings.Contains(out, "private-artifact block") {
		t.Fatalf("no work tree must print no ignore notice:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("a directory in no work tree gained .gitignore: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "CLAUDE.md")); statErr != nil {
		t.Fatalf("CLAUDE.md not compiled: %v", statErr)
	}
}
