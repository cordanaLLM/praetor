// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Positive and Negative: compile-context --verify detects uncompiled agent instruction files,
// ignores ignored ones, and permits compiled vendor targets.
func TestCompileContext_UncompiledInstructions_NegativeAndPositive(t *testing.T) {
	dir := evidenceContextFixture(t, "/.workingdir/\n")

	// Compile initial valid vendor targets so verify passes initially.
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("initial compile-context: %v\n%s", err, out)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("initial verify must pass: %v\n%s", err, out)
	}

	// 1. Untracked .github/instructions/review.instructions.md (not ignored) -> finding, fails.
	reviewFile := ".github/instructions/review.instructions.md"
	writeFixtureFile(t, dir, reviewFile, "# Review rules\n")
	_, err := runCompileContextCmd(t, dir, "--verify")
	if err == nil {
		t.Fatalf("verify must fail on uncompiled instruction file %s", reviewFile)
	}
	mustErrContain(t, err, "agent instruction file outside AGENTS.md: .github/instructions/review.instructions.md; move its text into AGENTS.md and run compile-context, or remove it")

	// 2. The same file listed in .gitignore -> no finding, verify passes.
	writeFixtureFile(t, dir, ".gitignore", "/.workingdir/\n.github/instructions/review.instructions.md\n")
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify must pass when instruction file is git-ignored: %v\n%s", err, out)
	}

	// Clean up review file.
	if err := os.Remove(filepath.Join(dir, reviewFile)); err != nil {
		t.Fatal(err)
	}

	// 3. .cursor/rules/extra.mdc -> finding, fails.
	extraCursor := ".cursor/rules/extra.mdc"
	writeFixtureFile(t, dir, extraCursor, "---\ndescription: extra\n---\n")
	_, err = runCompileContextCmd(t, dir, "--verify")
	if err == nil {
		t.Fatalf("verify must fail on uncompiled cursor rule %s", extraCursor)
	}
	mustErrContain(t, err, "agent instruction file outside AGENTS.md: .cursor/rules/extra.mdc; move its text into AGENTS.md and run compile-context, or remove it")

	// 4. Compiled hiss-invariants.mdc alone -> no finding, passes.
	if err := os.Remove(filepath.Join(dir, extraCursor)); err != nil {
		t.Fatal(err)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify must pass with only compiled hiss-invariants.mdc: %v\n%s", err, out)
	}
}
