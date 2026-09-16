package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"hiss-audit", "repo-adopt"} {
		dir := filepath.Join(root, filepath.FromSlash(canonicalSkillsRel), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: fixture\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, skillEntryName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, filepath.FromSlash(pluginManifestRel))
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"praetor"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Positive: the plugin ships every declared skill. It shipped none, while praetor's own
// harvester already read plugin skills from <plugin>/skills, so installing the plugin
// delivered six personas and not one of the eleven skills the repository declares.
func TestProjectPluginSkills_Positive_ShipsEveryDeclaredSkill(t *testing.T) {
	root := skillFixture(t)
	written, err := projectPluginSkills(root)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if written != 2 {
		t.Fatalf("expected 2 projected skills, got %d", written)
	}
	verified, err := verifyPluginSkills(root)
	if err != nil || verified != 2 {
		t.Fatalf("freshly projected skills do not verify: %d %v", verified, err)
	}
}

// Negative: a skill the repository does not declare must not ship.
func TestVerifyPluginSkills_Negative_RejectsAnOrphanSkill(t *testing.T) {
	root := skillFixture(t)
	if _, err := projectPluginSkills(root); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, filepath.FromSlash(pluginSkillsRel), "not-declared")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := verifyPluginSkills(root)
	if err == nil {
		t.Fatal("an undeclared skill was shipped without complaint")
	}
	if !strings.Contains(err.Error(), "not-declared") {
		t.Errorf("error does not name the orphan: %v", err)
	}
}

// Negative: a shipped copy that drifts from its declaration is reported.
func TestVerifyPluginSkills_Negative_RejectsADriftedCopy(t *testing.T) {
	root := skillFixture(t)
	if _, err := projectPluginSkills(root); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join(root, filepath.FromSlash(pluginSkillsRel), "hiss-audit", skillEntryName)
	if err := os.WriteFile(shipped, []byte("drifted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPluginSkills(root); err == nil {
		t.Fatal("a drifted plugin skill verified")
	}
}

// Boundary: a repository shipping no plugin manifest projects and verifies nothing rather
// than inventing a requirement it never declared.
func TestPluginSkills_Boundary_NoManifestIsNotAFailure(t *testing.T) {
	root := skillFixture(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(pluginManifestRel))); err != nil {
		t.Fatal(err)
	}
	written, err := projectPluginSkills(root)
	if err != nil || written != 0 {
		t.Errorf("projected %d skills without a plugin manifest: %v", written, err)
	}
	verified, err := verifyPluginSkills(root)
	if err != nil || verified != 0 {
		t.Errorf("verified %d skills without a plugin manifest: %v", verified, err)
	}
}
