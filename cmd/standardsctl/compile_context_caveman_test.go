package main

import (
	"errors"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// proseAgentsMD is the fixture AGENTS.md with a paragraph of prose below it: what an adopter's
// own part of AGENTS.md looks like before its caveman rewrite.
const proseAgentsMD = fixtureAgentsMD + "\n" + cavemanProse

// The gate lints on --verify only; compiling a prose AGENTS.md still writes the targets, so
// the author sees the lint fail on the next verify rather than losing the compile.
func TestCompileContextCaveman_Positive(t *testing.T) {
	dir := newContextFixture(t, false)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	out, err := runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "caveman lint passed:", "register block lines left to the renderer")
}

func TestCompileContextCaveman_Negative(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, "AGENTS.md", proseAgentsMD)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compiling prose must still write the targets: %v\n%s", err, out)
	}
	_, err := runCompileContextCmd(t, dir, "--verify")
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
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	if _, err := runCompileContextCmd(t, dir, "--verify"); !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("surfaces.agent = docs must not switch the gate off, got %v", err)
	}
}
