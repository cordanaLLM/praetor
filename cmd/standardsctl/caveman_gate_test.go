package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// fixtureSkill is a caveman-clean SKILL.md, used as the canonical (passing) fixture.
const fixtureSkill = "---\nname: example\ndescription: Example skill.\n---\n\n# Example\n\nRun `make verify-all` before turn end.\n"

func TestCavemanGatePersonasPositive(t *testing.T) {
	dir := newContextFixture(t, false)
	n, err := compiler.LintCanonicalPersonas(t.Context(), dir)
	if err != nil || n != 1 {
		t.Fatalf("compiler.LintCanonicalPersonas = %d, %v; want 1, nil", n, err)
	}
}

func TestCavemanGateSkillsPositive(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".agents/skills/example/SKILL.md", fixtureSkill)
	n, err := compiler.LintCanonicalSkillFiles(t.Context(), dir)
	if err != nil || n != 1 {
		t.Fatalf("compiler.LintCanonicalSkillFiles = %d, %v; want 1, nil", n, err)
	}
}

// TestCavemanGatePersonasNegative is the HISS-20 fixture pair: a persona that regresses to
// prose fails compiler.LintCanonicalPersonas directly, fails compile-context --verify and fails the
// audit gate, all three reproducing the same praetorctl caveman check verdict.
func TestCavemanGatePersonasNegative(t *testing.T) {
	dir := newContextFixture(t, false)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("initial compile-context: %v", err)
	}
	writeFixtureFile(t, dir, ".agents/agents/praetor-gatekeeper.md", cavemanProse)

	if _, err := compiler.LintCanonicalPersonas(t.Context(), dir); !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("compiler.LintCanonicalPersonas: want ErrAgentTextProse, got %v", err)
	}
	_, err := runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("compile-context --verify: want ErrAgentTextProse, got %v", err)
	}
	mustErrContain(t, err, filepath.Join(compiler.CanonicalAgentsRel, "praetor-gatekeeper.md"))
	if err := auditCavemanAgentSurfaces(t.Context(), dir); !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("audit gate: want ErrAgentTextProse, got %v", err)
	}
}

// TestCavemanGateSkillsNegative mirrors the persona case for a skill: prose fails the same
// three call sites (direct helper, compile-context --verify, audit).
func TestCavemanGateSkillsNegative(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".agents/skills/example/SKILL.md", fixtureSkill)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("initial compile-context: %v", err)
	}
	writeFixtureFile(t, dir, ".agents/skills/example/SKILL.md", cavemanProse)

	if _, err := compiler.LintCanonicalSkillFiles(t.Context(), dir); !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("compiler.LintCanonicalSkillFiles: want ErrAgentTextProse, got %v", err)
	}
	_, err := runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("compile-context --verify: want ErrAgentTextProse, got %v", err)
	}
	mustErrContain(t, err, filepath.Join(compiler.CanonicalSkillsRel, "example", compiler.SkillEntryName))
	if err := auditCavemanAgentSurfaces(t.Context(), dir); !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("audit gate: want ErrAgentTextProse, got %v", err)
	}
}

// TestCavemanGateBoundary covers the edges: no .agents/agents or .agents/skills directory at
// all (zero personas/skills is not a failure, matching listCanonicalAgents/listCanonicalSkills
// returning nil for an absent directory), and a persona exactly at AgentTextCeiling passes
// while one word over it fails on the ceiling alone.
func TestCavemanGateBoundary(t *testing.T) {
	empty := t.TempDir()
	if n, err := compiler.LintCanonicalPersonas(t.Context(), empty); err != nil || n != 0 {
		t.Fatalf("no .agents/agents: compiler.LintCanonicalPersonas = %d, %v; want 0, nil", n, err)
	}
	if n, err := compiler.LintCanonicalSkillFiles(t.Context(), empty); err != nil || n != 0 {
		t.Fatalf("no .agents/skills: compiler.LintCanonicalSkillFiles = %d, %v; want 0, nil", n, err)
	}
	if err := auditCavemanAgentSurfaces(t.Context(), empty); err != nil {
		t.Fatalf("audit gate over an empty root must not fail: %v", err)
	}

	dir := newContextFixture(t, false)
	at := strings.TrimSuffix(strings.Repeat("gate.\n", compiler.AgentTextCeiling), "\n")
	writeFixtureFile(t, dir, ".agents/agents/praetor-gatekeeper.md", at)
	if _, err := compiler.LintCanonicalPersonas(t.Context(), dir); err != nil {
		t.Fatalf("exactly at the ceiling must pass: %v", err)
	}
	over := strings.TrimSuffix(strings.Repeat("gate.\n", compiler.AgentTextCeiling+1), "\n")
	writeFixtureFile(t, dir, ".agents/agents/praetor-gatekeeper.md", over)
	if _, err := compiler.LintCanonicalPersonas(t.Context(), dir); !errors.Is(err, compiler.ErrAgentTextProse) {
		t.Fatalf("one word over the ceiling must fail: %v", err)
	}
}
