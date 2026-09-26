package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const selectionAgentsMD = "# Harness\n\nShared policy.\n"

func writeSelectionFixture(t *testing.T, manifest string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte(selectionAgentsMD), 0o600); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, agents
}

// Positive: agent_clients in the manifest beside the source limits what compile writes and
// what verify reads, so a projection the repository deleted is neither recreated nor missed.
func TestCompileContext_Positive_ManifestSelectsAgentClients(t *testing.T) {
	dir, agents := writeSelectionFixture(t, "version: 1\nagent_clients: [claude]\n")
	ctx := context.Background()
	tr := NewTranspiler()
	res, err := tr.CompileContext(ctx, agents)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].RelativePath != "CLAUDE.md" || len(res.NotApplicable) != 5 {
		t.Fatalf("compiled %v, not applicable %v", res.Files, res.NotApplicable)
	}
	if err := tr.WriteOutputsContext(ctx, res, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".windsurfrules")); !os.IsNotExist(err) {
		t.Fatalf("unselected projection written: %v", err)
	}
	verified, err := tr.VerifyCompiled(ctx, agents, dir)
	if err != nil {
		t.Fatalf("verify must not require unselected projections: %v", err)
	}
	if len(verified.Files) != 1 {
		t.Fatalf("verified %d projections, want 1", len(verified.Files))
	}
}

// Negative: an unknown client in the manifest fails compile and verify alike, and a manifest
// that does not parse is not read as "no selection".
func TestCompileContext_Negative_InvalidSelectionFails(t *testing.T) {
	ctx := context.Background()
	_, agents := writeSelectionFixture(t, "version: 1\nagent_clients: [claude, vim]\n")
	if err := NewTranspiler().VerifyContext(ctx, agents, filepath.Dir(agents)); err == nil || !strings.Contains(err.Error(), "vim") {
		t.Fatalf("unknown client not rejected: %v", err)
	}
	_, agents = writeSelectionFixture(t, "version: 1\nagent_clients: claude\n")
	if _, err := NewTranspiler().CompileContext(ctx, agents); err == nil {
		t.Fatal("a scalar agent_clients was read as no selection")
	}
}

// Boundary: no manifest and a manifest without the key both keep every projection, and a
// selection set on the transpiler wins over the manifest.
func TestCompileContext_Boundary_AbsentSelectionAndExplicitOverride(t *testing.T) {
	ctx := context.Background()
	for _, manifest := range []string{"", "version: 1\n"} {
		_, agents := writeSelectionFixture(t, manifest)
		res, err := NewTranspiler().CompileContext(ctx, agents)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != 6 || res.NotApplicable != nil {
			t.Fatalf("manifest %q: %d files, not applicable %v", manifest, len(res.Files), res.NotApplicable)
		}
	}
	_, agents := writeSelectionFixture(t, "version: 1\nagent_clients: [claude]\n")
	tr := NewTranspiler()
	tr.Clients = []string{"gemini", "codex"}
	res, err := tr.CompileContext(ctx, agents)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 || res.Files[0].RelativePath != ".gemini/GEMINI.md" {
		t.Fatalf("explicit selection ignored: %v", res.Files)
	}
}
