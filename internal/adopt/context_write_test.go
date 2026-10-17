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

// Positive: register.evidence.dir reaches every step of the write. A repository that keeps only
// .workingdir2/ private and sends evidence there keeps its .gitignore byte for byte, the
// spliced block names .workingdir2/evidence/, and compile-context --verify accepts the result.
func TestCompileAgentContext_Positive_ConfiguredEvidenceDir(t *testing.T) {
	root := newTestRepo(t, "context-write-evidence-dir")
	const rules = ".workingdir2/**\n"
	mustWrite(t, filepath.Join(root, ".gitignore"), rules)
	mustWrite(t, filepath.Join(root, manifestFile), "version: 1\nregister:\n  evidence:\n    dir: .workingdir2/evidence/\n")
	mustWrite(t, filepath.Join(root, agentsFile), contextWriteAgents)
	out, err := compileAgentContextIn(t, root)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if strings.Contains(out, "private-artifact block") || mustRead(t, filepath.Join(root, ".gitignore")) != rules {
		t.Fatalf("an ignored configured directory must leave .gitignore alone:\n%s", out)
	}
	agents := mustRead(t, filepath.Join(root, agentsFile))
	if !strings.Contains(agents, "file under `.workingdir2/evidence/`;") || strings.Contains(agents, ".workingdir/evidence/") {
		t.Fatalf("the spliced block must name the configured directory:\n%s", agents)
	}
	var verify strings.Builder
	if err := compiler.VerifyCompiledContext(t.Context(), &verify, compiler.NewTranspiler(), filepath.Join(root, agentsFile), root); err != nil {
		t.Fatalf("verify after the write: %v\n%s", err, verify.String())
	}
}

// Negative: a manifest whose evidence directory is refused compiles nothing and leaves
// .gitignore alone.
func TestCompileAgentContext_Negative_InvalidEvidenceDir(t *testing.T) {
	root := newTestRepo(t, "context-write-evidence-dir-invalid")
	mustWrite(t, filepath.Join(root, manifestFile), "version: 1\nregister:\n  evidence:\n    dir: ../outside/\n")
	mustWrite(t, filepath.Join(root, agentsFile), contextWriteAgents)
	_, err := compileAgentContextIn(t, root)
	if err == nil || !strings.Contains(err.Error(), "compiled no agent context") || !strings.Contains(err.Error(), "'..'") {
		t.Fatalf("want the refused directory named, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused directory still wrote .gitignore: %v", statErr)
	}
	if got := mustRead(t, filepath.Join(root, agentsFile)); got != contextWriteAgents {
		t.Fatalf("a refused write spliced AGENTS.md: %q", got)
	}
}
