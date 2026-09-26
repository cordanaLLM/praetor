package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// writeKitConfig writes a kit config fixture and returns its path.
func writeKitConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kit.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCompileFrameworkAssetsCommand_DispatchesAndWrites runs the command through the
// dispatch table, the path ADR-0007 clause 5 needed and BUG-694 found missing.
func TestCompileFrameworkAssetsCommand_DispatchesAndWrites(t *testing.T) {
	config := writeKitConfig(t, "kit_name: sveltesentio\nlanguage: svelte\nversion: 5.0.0\nrules: [Use runes]\ncomponents: [Button]\n")
	output := filepath.Join(t.TempDir(), "assets")
	stdout, err := captureStdout(t, func() error {
		return dispatchCommand("compile-framework-assets", []string{"--config", config, "--output", output})
	})
	if err != nil {
		t.Fatalf("compile-framework-assets error = %v", err)
	}
	for _, rel := range []string{"llms.txt", "llms-full.txt", filepath.Join(".agents", "rules", "sveltesentio.md"),
		filepath.Join("templates", ".framework-build.yaml"), filepath.Join("templates", "README.md")} {
		path := filepath.Join(output, rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("asset %s not written: %v", rel, err)
		}
		if !strings.Contains(stdout, path) {
			t.Errorf("stdout does not list %s:\n%s", path, stdout)
		}
	}
	full, err := os.ReadFile(filepath.Join(output, "llms-full.txt"))
	if err != nil || !strings.Contains(string(full), "Use runes") {
		t.Fatalf("llms-full.txt missing the kit rule: %q, %v", full, err)
	}
}

func TestCompileFrameworkAssetsCommand_RefusesMissingInput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "assets")
	config := writeKitConfig(t, "kit_name: kit\n")
	cases := map[string][]string{
		"no config":      {"--output", output},
		"no output":      {"--config", config},
		"blank config":   {"--config", "  ", "--output", output},
		"missing config": {"--config", filepath.Join(t.TempDir(), "absent.yaml"), "--output", output},
		"positional":     {"--config", config, "--output", output, "extra"},
	}
	for name, args := range cases {
		if err := runCompileFrameworkAssets(args); err == nil {
			t.Errorf("%s: compile-framework-assets accepted %q", name, args)
		}
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused runs created the output dir (stat err %v)", err)
	}
}

// TestCompileFrameworkAssetsCommand_RefusesEscapingKitName is the CLI half of BUG-618: a
// kit_name that is not one file-name component fails before anything is written.
func TestCompileFrameworkAssetsCommand_RefusesEscapingKitName(t *testing.T) {
	config := writeKitConfig(t, "kit_name: ../../escape\n")
	output := filepath.Join(t.TempDir(), "assets")
	err := runCompileFrameworkAssets([]string{"--config", config, "--output", output})
	if !errors.Is(err, compiler.ErrInvalidKitName) {
		t.Fatalf("error = %v, want ErrInvalidKitName", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output dir created for a refused kit (stat err %v)", statErr)
	}
}
