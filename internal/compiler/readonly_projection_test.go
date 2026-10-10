package compiler

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

func TestCompileReadOnlyContext_Positive(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "AGENTS.md")
	content := "# Project Harness\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n\n## Core Directives & Invariants\n\nRule 1.\n\n## Operational Rules\n\n10. **State ledger discipline (HISS-17).** Agents MUST maintain local `.workingdir` ledger every turn.\n    - Turn end: `praetorctl state sync .`\n\n11. **Diff-aware CI efficiency (HISS-18).**\n\n## Primary Verification Commands\n```bash\ngo test ./...\nmake verify-all\n```\n"
	if err := os.WriteFile(source, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	if err := CompileReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("CompileReadOnlyContext failed: %v", err)
	}

	// Verify file was written
	roPath := filepath.Join(dir, ReadOnlyFile)
	data, err := os.ReadFile(roPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", ReadOnlyFile, err)
	}

	text := string(data)
	if !strings.Contains(text, agentcontext.ReadOnlyBanner) {
		t.Errorf("read-only banner missing from %s", ReadOnlyFile)
	}
	if strings.Contains(text, "make verify-all") {
		t.Errorf("make verify-all still present in %s", ReadOnlyFile)
	}
	if strings.Contains(text, "state sync") {
		t.Errorf("state sync still present in %s", ReadOnlyFile)
	}

	// Verify VerifyReadOnlyContext passes
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("VerifyReadOnlyContext failed: %v", err)
	}
}

func TestVerifyReadOnlyContext_Negative(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "AGENTS.md")
	content := "# Project Harness\n\nRule 1.\n"
	if err := os.WriteFile(source, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	// Negative 1: file missing
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err == nil {
		t.Fatalf("expected error when %s is missing", ReadOnlyFile)
	}

	// Compile it first
	if err := CompileReadOnlyContext(t.Context(), source, dir); err != nil {
		t.Fatalf("CompileReadOnlyContext failed: %v", err)
	}

	// Negative 2: drift / out of sync
	roPath := filepath.Join(dir, ReadOnlyFile)
	if err := os.WriteFile(roPath, []byte("tampered content\n"), 0644); err != nil {
		t.Fatalf("failed to tamper %s: %v", ReadOnlyFile, err)
	}
	if err := VerifyReadOnlyContext(t.Context(), source, dir); err == nil {
		t.Fatalf("expected error when %s is out of sync", ReadOnlyFile)
	}
}

// TestCompileContextProjections_Negative_MissingSourceWritesNothing: blocker 2. A source that
// cannot be read fails with the "compilation failed" contract the CLI reports, and nothing is
// written: no vendor file, no read-only projection.
func TestCompileContextProjections_Negative_MissingSourceWritesNothing(t *testing.T) {
	root := t.TempDir()
	err := CompileContextProjections(t.Context(), io.Discard, NewTranspiler(), filepath.Join(root, "missing.md"), root)
	if err == nil || !strings.Contains(err.Error(), "compilation failed") {
		t.Fatalf("missing source: %v", err)
	}
	for _, rel := range []string{"CLAUDE.md", ReadOnlyFile} {
		if _, statErr := os.Stat(filepath.Join(root, rel)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%s written for a missing source: %v", rel, statErr)
		}
	}
}

// TestCompileContextProjections_Positive_ProjectionOfTheSplicedSnapshot: the read-only
// projection is compiled from the same snapshot as the vendor files, after the text register
// splice, so it matches the spliced source on disk and verify accepts it.
func TestCompileContextProjections_Positive_ProjectionOfTheSplicedSnapshot(t *testing.T) {
	root := skillFixture(t)
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Policy\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n")
	if err := compileFixture(t, root); err != nil {
		t.Fatalf("compile: %v", err)
	}
	spliced := readFixtureText(t, source)
	if !strings.Contains(spliced, "praetor:register:start") {
		t.Fatal("the write did not splice the register block, so the snapshot check proves nothing")
	}
	want, err := agentcontext.ReadOnlyProjection(spliced)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFixtureText(t, filepath.Join(root, ReadOnlyFile)); got != want {
		t.Fatalf("projection differs from the spliced source's:\n%s", got)
	}
	if err := VerifyReadOnlyContext(t.Context(), source, root); err != nil {
		t.Fatalf("verify rejects the written projection: %v", err)
	}
}

// TestCompileContextProjections_Boundary_RefusedProjectionTargetWritesNothing: a read-only
// projection target the writer refuses (a directory) leaves the source unspliced and writes no
// vendor file.
func TestCompileContextProjections_Boundary_RefusedProjectionTargetWritesNothing(t *testing.T) {
	root := skillFixture(t)
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, fixtureSource)
	if err := os.Mkdir(filepath.Join(root, ReadOnlyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := compileFixture(t, root); err == nil {
		t.Fatal("a directory at the projection target was written over")
	}
	if got := readFixtureText(t, source); got != fixtureSource {
		t.Fatalf("source spliced before a refused write:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vendor file written before a refused write: %v", err)
	}
}
