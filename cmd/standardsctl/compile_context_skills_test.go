// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// The register skills a repository carries are copied into the skill directory of each selected
// agent client that does not read .agents/skills (#235), and compile-context --verify and the
// audit's projection gate check those copies.

const cavemanSkillRel = ".agents/skills/caveman/SKILL.md"

// newSkillContextFixture is newContextFixture carrying this repository's caveman skill.
func newSkillContextFixture(t *testing.T) string {
	t.Helper()
	dir := newContextFixture(t, false)
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(cavemanSkillRel)))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, cavemanSkillRel, string(data))
	return dir
}

// Positive: compile-context copies the carried caveman skill into .claude/skills, names it in
// the register block, and --verify and the audit gate pass.
func TestCompileContext_Positive_ProjectsCarriedRegisterSkill(t *testing.T) {
	dir := newSkillContextFixture(t)
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[COMPILED] 1 client skill projections of .agents/skills")
	if got, want := readFixtureFile(t, dir, ".claude/skills/caveman/SKILL.md"), readFixtureFile(t, dir, cavemanSkillRel); got != want {
		t.Fatal("the client copy differs from the canonical caveman skill")
	}
	if agents := readFixtureFile(t, dir, "AGENTS.md"); !strings.Contains(agents, "`caveman` skill:") || strings.Contains(agents, "`social-text`") {
		t.Fatalf("the block must name the carried caveman skill and not the absent social-text:\n%s", agents)
	}
	out, err = runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "1 client skill projections verified")
	if err := auditAgentProjections(t.Context(), dir); err != nil {
		t.Fatalf("audit gate: %v", err)
	}
}

// Negative: an edited client copy fails --verify and the audit gate as projection drift.
func TestCompileContext_Negative_EditedClientSkillCopyFails(t *testing.T) {
	dir := newSkillContextFixture(t)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	writeFixtureFile(t, dir, ".claude/skills/caveman/SKILL.md", "---\nname: caveman\n---\n\n# Edited\n")
	if _, err := runCompileContextCmd(t, dir, "--verify"); !errors.Is(err, compiler.ErrAgentProjectionDrift) {
		t.Fatalf("verify: expected compiler.ErrAgentProjectionDrift, got %v", err)
	}
	if err := auditAgentProjections(t.Context(), dir); !errors.Is(err, compiler.ErrAgentProjectionDrift) {
		t.Fatalf("audit gate: expected compiler.ErrAgentProjectionDrift, got %v", err)
	}
}

// Boundary: a selection without Claude Code keeps no client copy, so none is written or
// required.
func TestCompileContext_Boundary_NoClaudeNoSkillCopy(t *testing.T) {
	dir := newSkillContextFixture(t)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [codex]\n")
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".claude written for a selection without Claude Code (stat err=%v)", err)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	if err := auditAgentProjections(t.Context(), dir); err != nil {
		t.Fatalf("audit gate: %v", err)
	}
}
