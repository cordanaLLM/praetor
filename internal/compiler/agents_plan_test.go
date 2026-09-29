package compiler

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planPersonaRoot is a root holding one canonical persona with content.
func planPersonaRoot(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".agents", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planner.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// shipPluginWithSkill makes root ship the praetor plugin (PluginManifestRel) and declare one
// canonical skill, so the plugin persona and skill copies are projection targets.
func shipPluginWithSkill(t *testing.T, root, skill string) {
	t.Helper()
	for rel, content := range map[string]string{
		PluginManifestRel:                         "{\"name\": \"praetor\"}\n",
		skillEntryRel(CanonicalSkillsRel, "lint"): skill,
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Positive: PlanAgentSurfaces returns the copies CompileAgentSurfaces writes, the plugin persona
// and skill copies included, with the same targets and content, and writes none of them.
func TestPlanAgentSurfaces_Positive_ListsCopiesWithoutWriting(t *testing.T) {
	const content = "---\nname: planner\n---\n# Planner\n"
	const skill = "---\nname: lint\n---\n# Lint\n"
	root := planPersonaRoot(t, content)
	shipPluginWithSkill(t, root, skill)
	planned, err := PlanAgentSurfaces(t.Context(), root)
	if err != nil {
		t.Fatalf("PlanAgentSurfaces: %v", err)
	}
	if len(planned) != 6 {
		t.Fatalf("planned %d copies, want 4 persona copies, the plugin persona and the plugin skill: %+v", len(planned), planned)
	}
	want := map[string]string{PluginAgentsRel + "/planner.md": content, skillEntryRel(PluginSkillsRel, "lint"): skill}
	for _, file := range planned {
		if expected, ok := want[file.RelativePath]; ok && file.Content != expected || !ok && file.Content != content {
			t.Errorf("%s content = %q", file.RelativePath, file.Content)
		}
		delete(want, file.RelativePath)
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.RelativePath))); !os.IsNotExist(err) {
			t.Errorf("PlanAgentSurfaces wrote %s (lstat err=%v)", file.RelativePath, err)
		}
	}
	if len(want) != 0 {
		t.Errorf("plan lacks the plugin copies %v", want)
	}
	written, err := CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err != nil {
		t.Fatalf("CompileAgentSurfaces: %v", err)
	}
	for i := range written {
		if written[i] != planned[i] {
			t.Fatalf("copy %d written %+v, planned %+v", i, written[i], planned[i])
		}
	}
	if _, err := VerifyAgentProjections(t.Context(), root); err != nil {
		t.Errorf("verify rejects what CompileAgentSurfaces wrote: %v", err)
	}
	if _, err := VerifyPluginSkills(t.Context(), root); err != nil {
		t.Errorf("verify rejects the plugin skill CompileAgentSurfaces wrote: %v", err)
	}
}

// Negative: PlanAgentSurfaces refuses what CompileAgentSurfaces refuses before writing, a
// cancelled or absent context, a symlinked persona directory and a symlinked plugin persona
// directory.
func TestPlanAgentSurfaces_Negative_RefusesLikeCompileAgentSurfaces(t *testing.T) {
	root := planPersonaRoot(t, "# Planner\n")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PlanAgentSurfaces(cancelled, root); err == nil {
		t.Fatal("cancelled context accepted")
	}
	var absent context.Context
	if _, err := PlanAgentSurfaces(absent, root); err == nil {
		t.Fatal("nil context accepted")
	}
	linkDirInRoot(t, root, ".claude")
	if _, err := PlanAgentSurfaces(t.Context(), root); err == nil {
		t.Fatal("symlinked persona directory accepted")
	}

	plugin := planPersonaRoot(t, "# Planner\n")
	shipPluginWithSkill(t, plugin, "# Lint\n")
	outside := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(plugin, filepath.FromSlash(PluginAgentsRel)))
	_, err := CompileAgentSurfaces(t.Context(), io.Discard, plugin)
	if err == nil || !strings.Contains(err.Error(), "target "+PluginAgentsRel+"/planner.md") {
		t.Fatalf("want the symlinked plugin persona directory refused, got %v", err)
	}
	if personaDirWritten(plugin, ".claude/agents") {
		t.Error("a persona copy was written before the plugin refusal")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("wrote through the symlinked plugin directory: %v (err=%v)", entries, err)
	}
}

// Boundary: a root without canonical personas plans nothing, without error, and a plugin
// manifest without canonical personas or skills plans nothing either.
func TestPlanAgentSurfaces_Boundary_NoPersonasPlansNothing(t *testing.T) {
	planned, err := PlanAgentSurfaces(t.Context(), t.TempDir())
	if err != nil || len(planned) != 0 {
		t.Fatalf("planned %+v, err %v; want none", planned, err)
	}
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(PluginManifestRel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	planned, err = PlanAgentSurfaces(t.Context(), root)
	if err != nil || len(planned) != 0 {
		t.Fatalf("plugin without sources planned %+v, err %v; want none", planned, err)
	}
}
