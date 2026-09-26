package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reference integrations under editors/ are generator output (BUG-600, BUG-625).

// Positive: `editors reference` writes the generator's reference tree and --verify accepts it.
func TestRunEditorsReference_Positive_WritesThenVerifies(t *testing.T) {
	root := t.TempDir()
	out, err := captureStdout(t, func() error { return runEditors([]string{"reference", "--path=" + root}) })
	if err != nil || !strings.Contains(out, "2 created") {
		t.Fatalf("reference write: %v\n%s", err, out)
	}
	for _, rel := range []string{"editors/neovim/lua/standards.lua", "editors/jetbrains/inspectionProfiles/standards.xml"} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); statErr != nil {
			t.Errorf("reference write left out %s: %v", rel, statErr)
		}
	}
	out, err = captureStdout(t, func() error { return runEditors([]string{"reference", "--verify", root}) })
	if err != nil || !strings.Contains(out, "[PASS] 2 reference") {
		t.Fatalf("reference verify after write: %v\n%s", err, out)
	}
}

// Negative: a hand-edited reference file fails --verify and names the file; an unknown flag
// is refused before anything is written.
func TestRunEditorsReference_Negative_HandEditFailsVerify(t *testing.T) {
	root := t.TempDir()
	if _, err := captureStdout(t, func() error { return runEditors([]string{"reference", "--path=" + root}) }); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "editors", "jetbrains", "inspectionProfiles", "standards.xml")
	if err := os.WriteFile(profile, []byte("<component />\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := captureStdout(t, func() error { return runEditors([]string{"reference", "--verify", "--path=" + root}) })
	if err == nil || !strings.Contains(err.Error(), "editors/jetbrains/inspectionProfiles/standards.xml") {
		t.Fatalf("hand-edited profile passed verify: %v", err)
	}
	empty := t.TempDir()
	if _, err := captureStdout(t, func() error { return runEditors([]string{"reference", "--check", empty}) }); err == nil {
		t.Error("unknown flag accepted")
	}
	entries, err := os.ReadDir(empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("refused invocation wrote %d entries", len(entries))
	}
}

// Boundary: the engine's own tracked tree verifies, which is the check `make verify-all` runs.
func TestRunEditorsReference_Boundary_EngineTreeIsCurrent(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return runEditors([]string{"reference", "--verify", "--path=" + filepath.Join("..", "..")})
	})
	if err != nil {
		t.Fatalf("engine editors/ tree drifted; run make editors-reference: %v\n%s", err, out)
	}
}
