// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Positive: a clean repository with only compiled vendor rules produces no findings.
func TestVerifyUncompiledInstructions_Context_Positive(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	writeTestFile(t, dir, ".cursor/rules/hiss-invariants.mdc", "# HISS\n")

	if err := VerifyUncompiledInstructions(t.Context(), dir); err != nil {
		t.Fatalf("expected nil error for compiled vendor targets, got %v", err)
	}
	files, err := UncompiledInstructionFiles(t.Context(), dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("expected no files, got %v (err=%v)", files, err)
	}
}

// Negative: committable instruction files outside AGENTS.md produce findings and fail verification.
func TestVerifyUncompiledInstructions_Context_Negative(t *testing.T) {
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	writeTestFile(t, dir, ".github/instructions/review.instructions.md", "# Review\n")
	writeTestFile(t, dir, ".cursor/rules/extra.mdc", "# Extra\n")

	err := VerifyUncompiledInstructions(t.Context(), dir)
	if err == nil {
		t.Fatal("expected failure on uncompiled instruction files, got nil")
	}
	if !errors.Is(err, ErrUncompiledInstruction) {
		t.Fatalf("expected ErrUncompiledInstruction, got %v", err)
	}
	for _, want := range []string{
		"agent instruction file outside AGENTS.md: .github/instructions/review.instructions.md; move its text into AGENTS.md and run compile-context, or remove it",
		"agent instruction file outside AGENTS.md: .cursor/rules/extra.mdc; move its text into AGENTS.md and run compile-context, or remove it",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing expected finding in error: want %q, got %q", want, err.Error())
		}
	}
}

// Boundary: ignored files, non-instruction files, and directories outside Git return no findings.
func TestVerifyUncompiledInstructions_Context_Boundary(t *testing.T) {
	// 1. Outside a Git repository
	outsideDir := t.TempDir()
	writeTestFile(t, outsideDir, ".github/instructions/review.instructions.md", "# Review\n")
	if err := VerifyUncompiledInstructions(t.Context(), outsideDir); err != nil {
		t.Fatalf("non-git directory must return nil, got %v", err)
	}

	// 2. Ignored files and non-matching filenames
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	writeTestFile(t, dir, ".gitignore", ".github/instructions/codacy.instructions.md\n.cursor/rules/codacy.mdc\n")
	writeTestFile(t, dir, ".github/instructions/codacy.instructions.md", "# Codacy\n")
	writeTestFile(t, dir, ".cursor/rules/codacy.mdc", "# Codacy\n")
	writeTestFile(t, dir, ".github/instructions/notes.txt", "notes\n")
	writeTestFile(t, dir, ".cursor/rules/readme.md", "# Readme\n")

	if err := VerifyUncompiledInstructions(t.Context(), dir); err != nil {
		t.Fatalf("ignored and non-matching files must return nil, got %v", err)
	}
}
