package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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
	if err := auditCavemanAgentSurfaces(t.Context(), dir, filepath.Join(dir, "AGENTS.md")); !errors.Is(err, compiler.ErrAgentTextProse) {
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
	if err := auditCavemanAgentSurfaces(t.Context(), dir, filepath.Join(dir, "AGENTS.md")); !errors.Is(err, compiler.ErrAgentTextProse) {
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
	if err := auditCavemanAgentSurfaces(t.Context(), empty, filepath.Join(empty, "AGENTS.md")); err != nil {
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

// nestedContextFixture is newContextFixture as a Git work tree, compiled once, with a clean
// nested AGENTS.md tracked at nested/AGENTS.md.
func nestedContextFixture(t *testing.T) string {
	t.Helper()
	dir := newContextFixture(t, false)
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("initial compile-context: %v\n%s", err, out)
	}
	writeFixtureFile(t, dir, "nested/AGENTS.md", fixtureSkill)
	testsupport.RunFixtureGit(t, dir, []string{"add", "--all"})
	return dir
}

// TestCavemanGateNestedContextPositive: a clean tracked nested AGENTS.md passes compile-context
// --verify and the audit gate, and both count it.
func TestCavemanGateNestedContextPositive(t *testing.T) {
	dir := nestedContextFixture(t)
	out, err := runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("compile-context --verify: %v\n%s", err, out)
	}
	mustContain(t, out, "1 nested AGENTS.md, 1 personas and 0 skills passed the caveman lint")
	out, err = captureStdout(t, func() error {
		return auditCavemanAgentSurfaces(t.Context(), dir, filepath.Join(dir, "AGENTS.md"))
	})
	if err != nil {
		t.Fatalf("audit gate: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Caveman lint verified (1 nested AGENTS.md")
}

// TestCavemanGateNestedContextNegative is the #311 sentinel pair: a tracked nested AGENTS.md
// that regresses to prose fails compile-context --verify and the audit gate, each naming the
// file, independently of the persona and skill sentinels above.
func TestCavemanGateNestedContextNegative(t *testing.T) {
	dir := nestedContextFixture(t)
	writeFixtureFile(t, dir, "nested/AGENTS.md", cavemanProse)

	_, err := runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrNestedContextProse) {
		t.Fatalf("compile-context --verify: want ErrNestedContextProse, got %v", err)
	}
	mustErrContain(t, err, filepath.Join("nested", "AGENTS.md"))
	err = auditCavemanAgentSurfaces(t.Context(), dir, filepath.Join(dir, "AGENTS.md"))
	if !errors.Is(err, compiler.ErrNestedContextProse) {
		t.Fatalf("audit gate: want ErrNestedContextProse, got %v", err)
	}
	mustErrContain(t, err, filepath.Join("nested", "AGENTS.md"))
}

// TestCavemanGateNestedContextBoundary: the gate reads only tracked files. The same prose in an
// ignored nested AGENTS.md is never read, so compile-context --verify and the audit gate pass.
func TestCavemanGateNestedContextBoundary(t *testing.T) {
	dir := nestedContextFixture(t)
	writeFixtureFile(t, dir, ".gitignore", readFixtureFile(t, dir, ".gitignore")+"/ignored/\n")
	writeFixtureFile(t, dir, "ignored/AGENTS.md", cavemanProse)
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("compile-context --verify read an ignored file: %v\n%s", err, out)
	}
	if err := auditCavemanAgentSurfaces(t.Context(), dir, filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("audit gate read an ignored file: %v", err)
	}
}
