package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// =========================================================================
// Positive 3D Test
// =========================================================================

func TestRunEditors_Positive_EditorsFlagFiltersToAntigravity(t *testing.T) {
	root := t.TempDir()

	out, err := captureStdout(t, func() error {
		return runEditors([]string{"generate", "--path=" + root, "--editors=agy"})
	})
	if err != nil {
		t.Fatalf("generate --editors=agy failed: %v (output: %s)", err, out)
	}

	settingsPath := filepath.Join(root, ".vscode", "settings.json")
	settings, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", settingsPath, readErr)
	}
	if !strings.Contains(string(settings), "antigravity.searchMaxWorkspaceFileCount") {
		t.Errorf("settings.json missing antigravity key: %s", settings)
	}
	if !strings.Contains(string(settings), "files.watcherExclude") {
		t.Errorf("settings.json missing files.watcherExclude: %s", settings)
	}

	// An editor outside the requested set (--editors=agy only) must not be generated.
	if _, statErr := os.Stat(filepath.Join(root, ".helix", "config.toml")); !os.IsNotExist(statErr) {
		t.Errorf("expected .helix/config.toml to be absent for --editors=agy, stat err: %v", statErr)
	}

	if _, err := captureStdout(t, func() error {
		return runEditors([]string{"verify", "--path=" + root, "--editors=agy"})
	}); err != nil {
		t.Errorf("verify --editors=agy failed after generate: %v", err)
	}
}

// =========================================================================
// Negative 3D Test
// =========================================================================

func TestRunEditors_Negative_UnknownEditorIDWritesNothing(t *testing.T) {
	root := t.TempDir()

	_, err := captureStdout(t, func() error {
		return runEditors([]string{"generate", "--path=" + root, "--editors=agy,notarealeditor"})
	})
	if err == nil {
		t.Fatalf("expected error for unknown editor id")
	}
	if !strings.Contains(err.Error(), "notarealeditor") {
		t.Errorf("error %q does not name the unknown id", err.Error())
	}

	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("read root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files written on unknown editor id, found: %v", entries)
	}
}

// =========================================================================
// Boundary 3D Test
// =========================================================================

func TestRunEditors_Boundary_EmptyEditorsFlagKeepsDefault(t *testing.T) {
	for _, args := range [][]string{
		{"generate", "--path=%s"},                  // flag omitted entirely
		{"generate", "--path=%s", "--editors="},    // flag given but empty
		{"generate", "--path=%s", "--editors=, ,"}, // flag with only separators
	} {
		root := t.TempDir()
		resolved := make([]string, len(args))
		for i, a := range args {
			resolved[i] = strings.ReplaceAll(a, "%s", root)
		}

		if _, err := captureStdout(t, func() error {
			return runEditors(resolved)
		}); err != nil {
			t.Fatalf("generate with args %v failed: %v", resolved, err)
		}

		// Default keeps every supported editor: spot-check one file per family instead of
		// enumerating every generator's output.
		for _, want := range []string{
			filepath.Join(".vscode", "settings.json"),
			filepath.Join(".idea", "inspectionProfiles", "standards.xml"),
			filepath.Join(".helix", "config.toml"),
			".editorconfig",
		} {
			if _, statErr := os.Stat(filepath.Join(root, want)); statErr != nil {
				t.Errorf("args %v: expected default-set file %s, stat err: %v", resolved, want, statErr)
			}
		}

		settings, readErr := os.ReadFile(filepath.Join(root, ".vscode", "settings.json"))
		if readErr != nil {
			t.Fatalf("read settings.json: %v", readErr)
		}
		if !strings.Contains(string(settings), "antigravity.searchMaxWorkspaceFileCount") {
			t.Errorf("args %v: default set (includes antigravity) missing antigravity key", resolved)
		}
	}
}

// =========================================================================
// Resolved complexity policy (#360)
// =========================================================================

func generatedMaxLoc(t *testing.T, root string) string {
	t.Helper()
	if _, err := captureStdout(t, func() error {
		return runEditors([]string{"generate", "--path=" + root, "--editors=jetbrains"})
	}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	profile, err := os.ReadFile(filepath.Join(root, ".idea", "inspectionProfiles", "standards.xml"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(profile), `name="maxLoc" value="`)
	if !ok {
		t.Fatalf("profile states no maxLoc:\n%s", profile)
	}
	value, _, _ := strings.Cut(after, `"`)
	return value
}

func TestRunEditors_Positive_ProjectsTheRepositoryPolicy(t *testing.T) {
	root := t.TempDir()
	manifest := "version: 1\nrepository:\n  owner: example\n  name: demo\noverrides:\n  complexity:\n    max_func_loc: 42\n"
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := generatedMaxLoc(t, root); got != "42" {
		t.Errorf("maxLoc = %s, want the repository's 42", got)
	}
}

func TestRunEditors_Boundary_UnadoptedWorkspaceGetsTheAuditLength(t *testing.T) {
	if got, want := generatedMaxLoc(t, t.TempDir()), fmt.Sprint(config.AuditMaxFuncLOC); got != want {
		t.Errorf("maxLoc = %s, want the audit length %s", got, want)
	}
}

func TestRunEditors_Negative_UnresolvablePolicyWritesNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := captureStdout(t, func() error {
		return runEditors([]string{"generate", "--path=" + root, "--editors=jetbrains"})
	})
	if err == nil || !strings.Contains(err.Error(), "resolve complexity policy") {
		t.Fatalf("corrupt manifest = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".idea")); !os.IsNotExist(statErr) {
		t.Errorf("a failed resolution wrote editor files: %v", statErr)
	}
}
