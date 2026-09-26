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

// generatedCyclomatic runs `editors generate` for JetBrains and returns its output and the
// cyclomatic limit (m_limit) the inspection profile states. It is the one resolved ceiling the
// profile carries: the length and statement limits sat on HISS04ComplexityLOC, an inspection no
// IDE provides, and now reach the editor through standards-lsp (BUG-656).
func generatedCyclomatic(t *testing.T, root string) (string, string) {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return runEditors([]string{"generate", "--path=" + root, "--editors=jetbrains"})
	})
	if err != nil {
		t.Fatalf("generate: %v (output: %s)", err, out)
	}
	profile, err := os.ReadFile(filepath.Join(root, ".idea", "inspectionProfiles", "standards.xml"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(profile), `name="m_limit" value="`)
	if !ok {
		t.Fatalf("profile states no m_limit:\n%s", profile)
	}
	limit, _, _ := strings.Cut(after, `"`)
	return out, limit
}

// ceilingCyclomatic is the HISS-04 cyclomatic ceiling as the JetBrains profile states it.
func ceilingCyclomatic() string {
	return fmt.Sprint(config.HISSComplexityCeiling().MaxCyclomatic)
}

func writeEditorsManifest(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const editorsManifest = "version: 1\nrepository:\n  owner: example\n  name: demo\n"

func TestRunEditors_Positive_ProjectsTheRepositoryPolicy(t *testing.T) {
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml", editorsManifest+"overrides:\n  complexity:\n    max_cyclomatic: 7\n")
	if _, got := generatedCyclomatic(t, root); got != "7" {
		t.Errorf("m_limit = %s, want the repository's 7 within the ceiling %s", got, ceilingCyclomatic())
	}
}

// An unadopted workspace and a manifest without a lock both state the HISS-04 ceiling; the
// lock-less case used to state the plan-preview baseline's cyclomatic 15.
func TestRunEditors_Boundary_UnlockedWorkspaceGetsTheCeiling(t *testing.T) {
	want := ceilingCyclomatic()
	if _, got := generatedCyclomatic(t, t.TempDir()); got != want {
		t.Errorf("unadopted m_limit = %s, want %s", got, want)
	}
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml", editorsManifest)
	if out, got := generatedCyclomatic(t, root); got != want || strings.Contains(out, "[WARN]") {
		t.Errorf("no-lock m_limit = %s, want %s without a warning:\n%s", got, want, out)
	}
}

// A policy that exists but does not resolve -- the lock `praetorctl init` writes, or a corrupt
// manifest -- still generates, states the ceiling tightened by any readable override, and warns.
func TestRunEditors_Negative_UnresolvablePolicyWarnsAndGenerates(t *testing.T) {
	initLocked := t.TempDir()
	writeEditorsManifest(t, initLocked, ".standards.yaml",
		editorsManifest+"profiles: [framework]\noverrides:\n  complexity:\n    max_cyclomatic: 7\n")
	writeEditorsManifest(t, initLocked, ".standards.lock", "# SemVer lockfile\nversion: 1\npinned_version: \"v0.0.0\"\n")
	corrupt := t.TempDir()
	writeEditorsManifest(t, corrupt, ".standards.yaml", "version: [\n")

	for name, tc := range map[string]struct {
		root string
		want string
	}{
		"init lock":        {initLocked, "7"},
		"corrupt manifest": {corrupt, ceilingCyclomatic()},
	} {
		out, got := generatedCyclomatic(t, tc.root)
		if got != tc.want {
			t.Errorf("%s: m_limit = %s, want %s", name, got, tc.want)
		}
		if !strings.Contains(out, "[WARN] repository policy unresolved") {
			t.Errorf("%s: fallback not stated:\n%s", name, out)
		}
	}
}
