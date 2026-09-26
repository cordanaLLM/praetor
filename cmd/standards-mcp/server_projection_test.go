package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPVerifyFailsOnPersonaDrift(t *testing.T) {
	srv, root := newFixtureServer(t)
	// Write a valid AGENTS.md so we can compile it
	// But let's just make it drift by changing the generated CLAUDE.md.
	// First, let's write to it normally.
	res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "."})
	if res.IsError {
		t.Fatalf("compile failed: %v", res.Content[0].Text)
	}

	// Drift it
	claudePath := filepath.Join(root, "CLAUDE.md")
	writePathFixture(t, claudePath, "drifted\n")

	// Verify
	res = callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": ".", "verify_only": true})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "Context verification failed") {
		t.Errorf("expected verify to fail on drift, got: %v", res)
	}
}

func TestMCPVerifyIgnoresWhitespaceDrift(t *testing.T) {
	srv, root := newFixtureServer(t)
	// compile normally
	res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "."})
	if res.IsError {
		t.Fatalf("compile failed: %v", res.Content[0].Text)
	}

	// Read and append spaces
	claudePath := filepath.Join(root, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	writePathFixture(t, claudePath, string(data)+"   \n\n  ")

	// Verify
	res = callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": ".", "verify_only": true})
	if res.IsError {
		t.Errorf("expected verify to pass on whitespace drift, got error: %v", res.Content[0].Text)
	}
}

func TestMCPWriteProjectsPluginAgentsAndSkills(t *testing.T) {
	srv, root := newFixtureServer(t)
	// Create plugin manifest
	writePathFixture(t, filepath.Join(root, ".agents/plugins/praetor/plugin.json"), "{}")

	// Create a canonical agent and skill
	writePathFixture(t, filepath.Join(root, ".agents/agents/test_agent.md"), "agent data")
	writePathFixture(t, filepath.Join(root, ".agents/skills/test_skill/SKILL.md"), "skill data")

	// Compile
	res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "."})
	if res.IsError {
		t.Fatalf("compile failed: %v", res.Content[0].Text)
	}

	// Verify plugin projection
	assertPathFixture(t, filepath.Join(root, ".agents/plugins/praetor/agents/test_agent.md"), "agent data")
	assertPathFixture(t, filepath.Join(root, ".agents/plugins/praetor/skills/test_skill/SKILL.md"), "skill data")
}

func TestMCPInRootSymlinkedPersonaTargetRefused(t *testing.T) {
	srv, root := newFixtureServer(t)

	// In-root symlinked persona
	agentPath := filepath.Join(root, ".agents/agents/symlinked.md")
	targetPath := filepath.Join(root, "victim.md")
	writePathFixture(t, targetPath, "victim data")
	if err := os.MkdirAll(filepath.Dir(agentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, agentPath); err != nil {
		t.Fatal(err)
	}

	// Try compiling
	res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "."})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "must be regular") {
		t.Errorf("expected failure on symlinked persona, got: %v", res)
	}

	// Try in-root symlinked target (vendor file)
	// Remove symlinked persona to test target
	if err := os.Remove(agentPath); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(root, "out")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeTarget := filepath.Join(targetDir, "CLAUDE.md")
	if err := os.Symlink(targetPath, claudeTarget); err != nil {
		t.Fatal(err)
	}

	res = callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out"})
	if !res.IsError || (!strings.Contains(res.Content[0].Text, "must be a regular file") && !strings.Contains(res.Content[0].Text, "escapes the confinement root")) {
		t.Errorf("expected failure on symlinked target, got: %v", res)
	}
}

func TestMCP65PersonasErrorBoundary(t *testing.T) {
	srv, root := newFixtureServer(t)

	// Create 65 personas
	for i := 0; i < 65; i++ {
		writePathFixture(t, filepath.Join(root, fmt.Sprintf(".agents/agents/agent%02d.md", i)), "data")
	}

	res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "."})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "agent directory exceeds 50 entries") {
		t.Errorf("expected 64 file limit error, got: %v", res)
	}
}
