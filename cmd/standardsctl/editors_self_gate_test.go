package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The engine gates its own editor selection (BUG-439, BUG-446): `make editors-verify` runs
// `praetorctl editors verify` against this checkout from verify-all. These tests run the same
// command, so the platform matrix exercises it on Linux, macOS and Windows through go test.

// engineCheckout is the checkout root, relative to this package.
var engineCheckout = filepath.Join("..", "..")

// engineSelectedFiles are the files the editors the engine selects generate.
var engineSelectedFiles = []string{".editorconfig", ".vscode/settings.json", ".vscode/extensions.json", ".vscode/tasks.json"}

// Positive: the checkout's selected editor files hold every managed value, and the
// hand-extended .editorconfig is reported as preserved rather than counted as verified.
func TestEditorsVerify_Positive_EngineSelectionIsCurrent(t *testing.T) {
	out, err := captureStdout(t, func() error { return runEditors([]string{"verify", "--path=" + engineCheckout}) })
	if err != nil {
		t.Fatalf("engine editor files drifted; run `praetorctl editors generate`: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Managed requirements verified", "[UNVERIFIED]", "[.editorconfig]")
}

// Negative: a selected file that is missing, or that lost a managed value, fails the gate
// and names the file.
func TestEditorsVerify_Negative_DriftedSelectionFailsGate(t *testing.T) {
	for name, tc := range map[string]struct {
		drift func(root string) error
		want  string
	}{
		"missing .editorconfig": {want: ".editorconfig", drift: func(root string) error {
			return os.Remove(filepath.Join(root, ".editorconfig"))
		}},
		"settings lost a managed value": {want: ".vscode/settings.json", drift: func(root string) error {
			return os.WriteFile(filepath.Join(root, ".vscode", "settings.json"), []byte("{}\n"), 0o600)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			root := copyEngineSelection(t)
			if _, err := captureStdout(t, func() error { return runEditors([]string{"verify", "--path=" + root}) }); err != nil {
				t.Fatalf("an undrifted copy failed verify: %v", err)
			}
			if err := tc.drift(root); err != nil {
				t.Fatal(err)
			}
			_, err := captureStdout(t, func() error { return runEditors([]string{"verify", "--path=" + root}) })
			if err == nil || !strings.Contains(err.Error(), "out of sync") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("drifted selection passed the gate: %v", err)
			}
		})
	}
}

// Boundary: the engine declares an explicit selection, so the editors it leaves out are named
// and skipped. The tracked lua/standards.lua is out of sync with the generator, and the gate
// still passes because Neovim is not selected.
func TestEditorsVerify_Boundary_UnselectedEditorsAreSkipped(t *testing.T) {
	declared := engineDeclaredEditors(t)
	if !slices.Equal(declared, []string{"universal", "vscode"}) {
		t.Fatalf("engine editors selection = %v, want [universal vscode]", declared)
	}
	out, err := captureStdout(t, func() error { return runEditors([]string{"verify", "--path=" + engineCheckout}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[NOT_APPLICABLE] Editors not selected: cursor, windsurf, jetbrains, neovim,")
}

func engineDeclaredEditors(t *testing.T) []string {
	t.Helper()
	declared, err := config.LoadDeclaredTooling(context.Background(), engineCheckout)
	if err != nil {
		t.Fatalf("read engine editors selection: %v", err)
	}
	return declared.Editors
}

// copyEngineSelection copies the engine's selected editor files into a temporary workspace
// whose manifest declares the same selection.
func copyEngineSelection(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml",
		editorsManifest+"editors: ["+strings.Join(engineDeclaredEditors(t), ", ")+"]\n")
	for _, rel := range engineSelectedFiles {
		data, err := os.ReadFile(filepath.Join(engineCheckout, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
