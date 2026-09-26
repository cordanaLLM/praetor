package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
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

// allPersonaDirs is every agent client's persona directory, the set an absent agent_clients
// keeps.
func allPersonaDirs(t *testing.T) []string {
	t.Helper()
	dirs, excluded, err := agentcontext.PersonaDirs(nil)
	if err != nil || len(excluded) != 0 || len(dirs) == 0 {
		t.Fatalf("PersonaDirs(nil) = %v, %v, %v", dirs, excluded, err)
	}
	return dirs
}

// Positive: agent_clients also limits the persona copies. compile-context writes only the
// selected client's directory and the plugin copy, names the rest not applicable, leaves a
// stale copy in an unselected directory untouched, and --verify and the audit gate neither
// require nor read the unselected directories.
func TestCompileContext_Positive_AgentClientsSelectPersonaDirs(t *testing.T) {
	dir := newContextFixture(t, true)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [claude]\n")
	writeFixtureFile(t, dir, ".codex/agents/praetor-gatekeeper.md", "stale\n")
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[COMPILED] 1 autonomous agent vendor projections",
		"[NOT_APPLICABLE] .github/agents", "[NOT_APPLICABLE] .gemini/agents", "[NOT_APPLICABLE] .codex/agents")
	if got := readFixtureFile(t, dir, ".claude/agents/praetor-gatekeeper.md"); got != fixturePersona {
		t.Fatalf("selected persona copy = %q", got)
	}
	for _, rel := range []string{".github/agents", ".gemini/agents"} {
		if util.DirExists(filepath.Join(dir, filepath.FromSlash(rel))) {
			t.Errorf("unselected persona directory %s written", rel)
		}
	}
	if got := readFixtureFile(t, dir, ".codex/agents/praetor-gatekeeper.md"); got != "stale\n" {
		t.Errorf("an unselected persona copy was rewritten: %q", got)
	}
	out, err = runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "2 persona projections verified", "[NOT_APPLICABLE] .gemini/agents")
	if err := auditAgentProjections(t.Context(), dir); err != nil {
		t.Fatalf("audit gate: %v", err)
	}
}

// Negative: the selection narrows what is checked, never how. A loosened copy in a selected
// directory still fails, and an unknown client fails persona verification instead of reading
// as "every client".
func TestCompileContext_Negative_AgentClientsStillVerifySelectedPersonas(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [copilot]\n")
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	writeFixtureFile(t, dir, ".github/agents/praetor-gatekeeper.md", "# Gatekeeper\n\nReceipts are optional.\n")
	if err := auditAgentProjections(t.Context(), dir); !errors.Is(err, ErrAgentProjectionDrift) {
		t.Fatalf("audit gate: expected ErrAgentProjectionDrift, got %v", err)
	}
	writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: [copilot, vim]\n")
	_, err := verifyAgentProjections(t.Context(), dir)
	mustErrContain(t, err, "unknown agent client id(s): vim")
}

// Boundary: an empty selection and one naming only clients that read no personas (Cursor,
// Windsurf) both keep no persona directory; the plugin copy is not a client surface and stays.
func TestCompileContext_Boundary_AgentClientsWithoutPersonaDirs(t *testing.T) {
	for _, selection := range []string{"[]", "[cursor, windsurf]"} {
		dir := newContextFixture(t, true)
		writeFixtureFile(t, dir, ".standards.yaml", clientsManifest+"agent_clients: "+selection+"\n")
		out, err := runCompileContextCmd(t, dir)
		if err != nil {
			t.Fatalf("%s: compile-context: %v\n%s", selection, err, out)
		}
		for _, rel := range allPersonaDirs(t) {
			if util.DirExists(filepath.Join(dir, filepath.FromSlash(rel))) {
				t.Errorf("%s: persona directory %s written", selection, rel)
			}
			mustContain(t, out, "[NOT_APPLICABLE] "+rel)
		}
		if n, err := verifyAgentProjections(t.Context(), dir); err != nil || n != 1 {
			t.Fatalf("%s: verifyAgentProjections = %d, %v; want 1 (the plugin copy), nil", selection, n, err)
		}
	}
}
