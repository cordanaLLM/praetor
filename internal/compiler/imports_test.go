package compiler

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

func TestRepositoryImportsCompileWriteAndVerify(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Policy\n@docs/x.md\n")
	writeOutputFixture(t, filepath.Join(root, "docs/x.md"), "@docs/y.md")
	writeOutputFixture(t, filepath.Join(root, "docs/y.md"), "Run gate.\n")
	tr := NewTranspiler()
	res, err := tr.CompileContext(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputsContext(t.Context(), res, root); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.VerifyCompiled(t.Context(), source, root); err != nil {
		t.Fatal(err)
	}
	writeOutputFixture(t, filepath.Join(root, "docs/y.md"), strings.Repeat("x", 100000))
	_, err = tr.VerifyCompiled(t.Context(), source, root)
	assertCompilerImportError(t, err, ".gemini/GEMINI.md", "bytes > 24000", "docs/y.md")
}

func assertCompilerImportError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("planted invalid context accepted")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestCompileAndVerifyRefuseOversizedGeminiProjection(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, strings.Repeat("x", 25000-len(agentcontext.ProjectionFrame(".gemini/GEMINI.md"))))
	tr := NewTranspiler()
	tr.Clients = []string{"gemini"}
	_, err := tr.CompileContext(t.Context(), source)
	assertCompilerImportError(t, err, ".gemini/GEMINI.md", "25000 bytes > 24000")
	_, err = tr.VerifyCompiled(t.Context(), source, root)
	assertCompilerImportError(t, err, ".gemini/GEMINI.md", "25000 bytes > 24000")
	_, err = compileSource(t.Context(), tr, source)
	assertCompilerImportError(t, err, ".gemini/GEMINI.md", "client size cap")
}

func TestProjectDocCapIncludesKnownNestedContexts(t *testing.T) {
	for _, size := range []int{32767, 32768, 32769} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			root := nestedRepo(t, map[string]string{"sub/AGENTS.md": strings.Repeat("x", size-len(lintTerseAgents))})
			tr := NewTranspiler()
			tr.Clients = []string{"codex"}
			_, err := tr.CompileContext(t.Context(), filepath.Join(root, "AGENTS.md"))
			if size > 32768 {
				assertCompilerImportError(t, err, "AGENTS.md", "32769 bytes > 32768")
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// compileCodexNested compiles a repository holding files for Codex, whose cap is the chain's.
func compileCodexNested(t *testing.T, files map[string]string) error {
	t.Helper()
	tr := NewTranspiler()
	tr.Clients = []string{"codex"}
	_, err := tr.CompileContext(t.Context(), filepath.Join(nestedRepo(t, files), "AGENTS.md"))
	return err
}

// Positive (engine-golusoris-gate-skew-20261030): a client loads one path, so sibling packages
// whose bytes add up far past the cap pass.
func TestProjectDocCapChain_Positive_SiblingsDoNotAddUp(t *testing.T) {
	files := map[string]string{}
	for i := range 5 {
		files[fmt.Sprintf("pkg%d/AGENTS.md", i)] = strings.Repeat("a", 20000)
	}
	if err := compileCodexNested(t, files); err != nil {
		t.Fatalf("sibling packages totaling 100k bytes must pass the chain cap: %v", err)
	}
}

// Negative: one path over the cap is refused, and the refusal names the file to trim from.
func TestProjectDocCapChain_Negative_DeepChainRefusedAndNamed(t *testing.T) {
	err := compileCodexNested(t, map[string]string{
		"a/AGENTS.md":     strings.Repeat("a", 15000),
		"a/b/AGENTS.md":   strings.Repeat("b", 15000),
		"a/b/c/AGENTS.md": strings.Repeat("c", 10000),
	})
	assertCompilerImportError(t, err, "AGENTS.md", "client size cap", "a/b/c/AGENTS.md")
}

// Boundary: a chain exactly at the cap passes, one byte more is refused.
func TestProjectDocCapChain_Boundary_ExactCap(t *testing.T) {
	part1 := 10000
	part2 := 32768 - len(lintTerseAgents) - part1
	files := func(second int) map[string]string {
		return map[string]string{"a/AGENTS.md": strings.Repeat("a", part1), "a/b/AGENTS.md": strings.Repeat("b", second)}
	}
	if err := compileCodexNested(t, files(part2)); err != nil {
		t.Fatalf("a chain exactly at the cap must pass: %v", err)
	}
	assertCompilerImportError(t, compileCodexNested(t, files(part2+1)), "AGENTS.md", "32769 bytes > 32768")
}

func TestRepositoryImportReadFailuresNamePath(t *testing.T) {
	for _, name := range []string{"missing.md", "directory", "huge.md"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "AGENTS.md")
			writeCanonicalFixture(t, source, "@"+name)
			writeOutputFixture(t, filepath.Join(root, "directory/child.md"), "Run gate.")
			writeOutputFixture(t, filepath.Join(root, "huge.md"), strings.Repeat("x", (1<<20)+1))
			_, err := NewTranspiler().CompileContext(t.Context(), source)
			assertCompilerImportError(t, err, name, "missing or unreadable")
		})
	}
}

func TestCompileContextProjectionsRefusesImportBeforeWrites(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Policy\n@bulk.md\n")
	writeOutputFixture(t, filepath.Join(root, "bulk.md"), strings.Repeat("x", 100000))
	var output bytes.Buffer
	_, err := CompileVendorTargets(t.Context(), &output, NewTranspiler(), source, root)
	assertCompilerImportError(t, err, "bulk.md", ".gemini/GEMINI.md", "client size cap")
	if output.Len() != 0 {
		t.Fatalf("refused compilation reported writes: %s", &output)
	}
}
