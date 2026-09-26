package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifying that every canonical persona has a projection is only half a check. A file in a
// projection directory that matches no canonical persona is never read, so it is never
// compared, so it can say anything. Six such orphans accumulated in the plugin directory and
// one had drifted to hardcode an absolute developer path in output that ships to other
// machines; nothing reported it because nothing ever looked.
func TestVerifyAgentProjections_Positive_RejectsAnOrphanProjection(t *testing.T) {
	root := projectionFixture(t)
	orphan := filepath.Join(root, ".agents", "plugins", "praetor", "agents", "praetor_auditor.md")
	if err := os.WriteFile(orphan, []byte("stale copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := verifyAgentProjections(t.Context(), root)
	if err == nil {
		t.Fatal("an orphan projection was accepted")
	}
	if !strings.Contains(err.Error(), "praetor_auditor.md") || !strings.Contains(err.Error(), "no canonical persona") {
		t.Errorf("error does not name the orphan or the reason: %v", err)
	}
}

// Negative: a tree whose projections all correspond to canonical personas verifies.
func TestVerifyAgentProjections_Negative_AcceptsAMatchingTree(t *testing.T) {
	root := projectionFixture(t)
	verified, err := verifyAgentProjections(t.Context(), root)
	if err != nil {
		t.Fatalf("a matching tree was rejected: %v", err)
	}
	if verified == 0 {
		t.Fatal("reported zero verified projections, so it proved nothing")
	}
}

// Boundary: a non-markdown file is not a persona, and an absent projection directory is not
// an error for a repository that ships no plugin.
func TestVerifyAgentProjections_Boundary_IgnoresNonPersonaFilesAndAbsentDirs(t *testing.T) {
	root := projectionFixture(t)
	notes := filepath.Join(root, ".agents", "plugins", "praetor", "agents", "README.txt")
	if err := os.WriteFile(notes, []byte("not a persona\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAgentProjections(t.Context(), root); err != nil {
		t.Errorf("a non-persona file was treated as a projection: %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".agents", "plugins", "praetor", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAgentProjections(t.Context(), root); err != nil {
		t.Errorf("a repository shipping no plugin was rejected: %v", err)
	}
}

// projectionFixture builds a minimal tree with one canonical persona projected everywhere.
func projectionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	persona := []byte("---\nname: praetor-auditor\n---\n\nBody.\n")
	dirs := append([]string{".agents/agents", ".agents/plugins/praetor/agents"}, allPersonaDirs(t)...)
	for _, dir := range dirs {
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "praetor-auditor.md"), persona, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(".agents/plugins/praetor/plugin.json")),
		[]byte(`{"name":"praetor"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
