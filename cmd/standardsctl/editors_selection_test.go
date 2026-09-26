package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editorsSelectionRun(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return runEditors(append(args, "--path="+root))
	})
}

func assertAbsent(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s should not exist: %v", rel, err)
		}
	}
}

// Positive: editors: [vscode] in the manifest writes only the VS Code family's file, verifies
// against that selection, names the rest as not applicable, and --editors still overrides it.
func TestRunEditors_Positive_ManifestSelectsEditors(t *testing.T) {
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml", editorsManifest+"editors: [vscode]\n")
	out, err := editorsSelectionRun(t, root, "generate")
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".vscode", "settings.json")); err != nil {
		t.Fatalf("selected editor not generated: %v", err)
	}
	assertAbsent(t, root, ".editorconfig", ".helix/config.toml", ".idea", ".zed/settings.json", ".nvim.lua")
	if !strings.Contains(out, "[NOT_APPLICABLE] Editors not selected: universal, cursor,") {
		t.Errorf("unselected editors not named:\n%s", out)
	}
	if out, err := editorsSelectionRun(t, root, "verify"); err != nil {
		t.Fatalf("verify must check only the selection: %v\n%s", err, out)
	}
	if out, err := editorsSelectionRun(t, root, "generate", "--editors=helix"); err != nil {
		t.Fatalf("generate --editors=helix: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".helix", "config.toml")); err != nil {
		t.Fatalf("--editors did not override the manifest: %v", err)
	}
}

// Negative: an unknown manifest id, or a manifest that cannot be read, fails before anything
// is written instead of falling back to every editor.
func TestRunEditors_Negative_InvalidManifestSelectionWritesNothing(t *testing.T) {
	for name, body := range map[string]string{
		"unknown id": editorsManifest + "editors: [vscode, notepad]\n",
		"corrupt":    "version: [\n",
	} {
		root := t.TempDir()
		writeEditorsManifest(t, root, ".standards.yaml", body)
		if out, err := editorsSelectionRun(t, root, "generate"); err == nil {
			t.Fatalf("%s: generate succeeded:\n%s", name, out)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Errorf("%s: files written on a rejected selection: %v", name, entries)
		}
	}
}

// Boundary: editors: [] selects no editor, so generate and verify both succeed having written
// and read nothing, and say so.
func TestRunEditors_Boundary_EmptyManifestSelectionIsNone(t *testing.T) {
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml", editorsManifest+"editors: []\n")
	for _, sub := range []string{"generate", "verify"} {
		out, err := editorsSelectionRun(t, root, sub)
		if err != nil {
			t.Fatalf("%s: %v\n%s", sub, err, out)
		}
		if !strings.Contains(out, "No editor is selected") {
			t.Errorf("%s did not state the empty selection:\n%s", sub, out)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("empty selection wrote files: %v", entries)
	}
}
