package main

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const volatileHeadAgents = "# Harness\n\n<!-- praetor:head -->\nBuilt 2026-10-07T12:30:00Z.\n\n<!-- praetor:tail -->\nCommands.\n"

// compiledFixtureServer sets up a fixture server with source as AGENTS.md and compiles it
// in place, so vendor targets are in sync and only the stability gate can refuse it.
func compiledFixtureServer(t *testing.T, source string) (*Server, string) {
	t.Helper()
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "AGENTS.md", source)
	if _, err := compiler.SyncRegisterBlock(t.Context(), root, filepath.Join(root, "AGENTS.md"), true); err != nil {
		t.Fatalf("splice fixture text register: %v", err)
	}
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("compile fixture AGENTS.md: %v", err)
	}
	if err := tr.WriteOutputs(res, root); err != nil {
		t.Fatalf("write fixture vendor targets: %v", err)
	}
	return srv, root
}

// TestMCPAuditAgentContext_Negative_RefusesVolatileHeadBand proves (rule 13) that
// standards_audit refuses a planted volatile head token.
func TestMCPAuditAgentContext_Negative_RefusesVolatileHeadBand(t *testing.T) {
	srv, _ := compiledFixtureServer(t, volatileHeadAgents)
	audit := callTool(t, srv, "standards_audit", nil)
	expectError(t, "volatile head audit", audit, "Agent context cache stability")
	expectError(t, "volatile head token", audit, "timestamp")
}

// TestMCPAuditAgentContext_Boundary_UnmarkedSourceWarns proves that standards_audit
// prints the unlayered warning for an unmarked source and does not fail on it.
func TestMCPAuditAgentContext_Boundary_UnmarkedSourceWarns(t *testing.T) {
	srv, _ := newFixtureServer(t)
	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "unmarked source audit pass", audit, "passed: 9/9")
	expectText(t, "unmarked source warns", audit, compiler.UnlayeredWarning)
}
