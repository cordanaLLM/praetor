package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const clientsManifest = "version: 1\nrepository:\n  owner: example\n  name: demo\n"

// Positive: agent_clients limits the vendor files compile-context writes, --verify reads
// only those, and both runs name what the selection leaves out.
func TestCompileContext_Positive_AgentClientsSelectProjections(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [claude, codex]\n")
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[COMPILED] CLAUDE.md", "[COMPILED] .codex/rules.md", "[NOT_APPLICABLE] .windsurfrules")
	for _, rel := range []string{".windsurfrules", ".gemini/GEMINI.md", ".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md"} {
		if util.FileExists(filepath.Join(dir, filepath.FromSlash(rel))) {
			t.Errorf("unselected projection %s written", rel)
		}
	}
	out, err = runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "[NOT_APPLICABLE] .gemini/GEMINI.md")
}

// Negative: an unknown client fails compile-context before any vendor file is written.
func TestCompileContext_Negative_UnknownAgentClientWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [claude, vim]\n")
	_, err := runCompileContextCmd(t, dir)
	mustErrContain(t, err, "unknown agent client id(s): vim")
	if util.FileExists(filepath.Join(dir, "CLAUDE.md")) {
		t.Fatal("a rejected selection still wrote CLAUDE.md")
	}
}

// Boundary: agent_clients: [] keeps no projection, so a deleted CLAUDE.md is not recreated and
// --verify passes without it.
func TestCompileContext_Boundary_EmptyAgentClientsKeepsNone(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: []\n")
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md written for an empty selection: %v", err)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
}
