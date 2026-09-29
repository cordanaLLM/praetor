package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// proseAgentsMD is the fixture AGENTS.md with a paragraph of prose below it: what an adopter's
// own part of AGENTS.md looks like before its caveman rewrite.
const proseAgentsMD = fixtureAgentsMD + "\n" + cavemanProse

// The write lints too: compiling a prose AGENTS.md still writes the targets, then fails on the
// lint, so the author loses no compile and never reads success the next verify contradicts.
func TestCompileContextCaveman_Positive(t *testing.T) {
	dir := newContextFixture(t, false)
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "caveman lint passed:", "Cross-agent context transpilation completed successfully.")
	out, err = runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "caveman lint passed:", "register block lines left to the renderer")
}

func TestCompileContextCaveman_Negative(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, "AGENTS.md", proseAgentsMD)
	out, err := runCompileContextCmd(t, dir)
	if !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("compiling prose must fail on the lint, got %v\n%s", err, out)
	}
	mustErrContain(t, err, "context written, but compile-context --verify will fail")
	mustErrContain(t, err, "C1 article-density")
	if strings.Contains(out, "completed successfully") {
		t.Fatalf("a failed lint must not print success:\n%s", out)
	}
	if !strings.Contains(readFixtureFile(t, dir, "CLAUDE.md"), "Two implementations of one behavior") {
		t.Fatal("compiling prose must still write the targets")
	}
	_, err = runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("prose AGENTS.md must fail --verify, got %v", err)
	}
	mustErrContain(t, err, "C1 article-density")
}

// There is no opt-out: a manifest that writes the context surface in another register is
// rejected before anything is written, and surfaces.agent never reaches the gate.
func TestCompileContextCaveman_Boundary(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, "AGENTS.md", proseAgentsMD)
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    context: docs\n")
	_, err := runCompileContextCmd(t, dir)
	mustErrContain(t, err, `register surface "context" is fixed to internal`)
	if readFixtureFile(t, dir, "AGENTS.md") != proseAgentsMD {
		t.Fatal("a rejected manifest must leave the source untouched")
	}

	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    agent: docs\n")
	if out, err := runCompileContextCmd(t, dir); !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("surfaces.agent = docs must not switch the write's gate off, got %v\n%s", err, out)
	}
	if _, err := runCompileContextCmd(t, dir, "--verify"); !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("surfaces.agent = docs must not switch the gate off, got %v", err)
	}
}
