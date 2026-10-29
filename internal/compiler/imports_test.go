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
