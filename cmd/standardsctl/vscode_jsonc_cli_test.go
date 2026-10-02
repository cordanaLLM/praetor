package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #316, at the commands an adopter runs: VS Code reads its .vscode files as JSON with
// Comments, so `flavor audit` counts a commented settings file valid, `editors verify` passes
// one holding every managed value, and `editors generate` refuses to rewrite one it would have
// to add values to.

// commentedGenerated adds a header comment and a trailing comma to a generated JSON object.
func commentedGenerated(t *testing.T, generated string) string {
	t.Helper()
	body, found := strings.CutSuffix(strings.TrimRight(generated, "\n"), "\n}")
	if !found {
		t.Fatalf("generated editor JSON is not an indented object:\n%s", generated)
	}
	return "// adopter note\n" + body + ",\n}\n"
}

func TestFlavorAudit_Positive_CommentedVSCodeSettingsPass(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "ruff.toml", "line-length = 100\n")
	writeFixtureFile(t, dir, ".vscode/settings.json",
		"// workspace settings\n{\n  /* interpreter */\n  \"python.defaultInterpreterPath\": \".venv/bin/python\", // venv\n}\n")
	out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir, "--flavor=python-ml"}) })
	if err != nil {
		t.Fatalf("flavor audit of a commented settings.json: %v\n%s", err, out)
	}
	mustContain(t, out, "Score:       100.0%", "Passed:      true", "Settings:    1/1 valid")
	if strings.Contains(out, "Missing or Invalid Settings") {
		t.Errorf("a commented settings.json was named invalid:\n%s", out)
	}
}

// Negative: a settings file that is not valid JSON with Comments either still fails and is named.
func TestFlavorAudit_Negative_BrokenVSCodeSettingsStillFail(t *testing.T) {
	for name, settings := range map[string]string{
		"duplicate key":      "{\n  // twice\n  \"editor.tabSize\": 2,\n  \"editor.tabSize\": 4\n}\n",
		"unterminated block": "{\n  \"editor.tabSize\": 2 /* never closed\n}\n",
	} {
		dir := t.TempDir()
		writeFixtureFile(t, dir, "ruff.toml", "line-length = 100\n")
		writeFixtureFile(t, dir, ".vscode/settings.json", settings)
		out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir, "--flavor=python-ml"}) })
		if err == nil || !strings.Contains(err.Error(), "flavor audit failed (score: 50.0%)") {
			t.Errorf("%s: want a failed audit at 50%%, got %v\n%s", name, err, out)
		}
		mustContain(t, out, "Missing or Invalid Settings", ".vscode/settings.json")
	}
}

// Boundary: generate, comment, verify, generate again. The commented files are in sync, so
// neither command changes or refuses them; with one managed value taken out, verify names the
// file and generate refuses to rewrite it, naming the file and the comments it would lose.
func TestRunEditors_Boundary_CommentedVSCodeFiles(t *testing.T) {
	root := t.TempDir()
	writeEditorsManifest(t, root, ".standards.yaml", editorsManifest+"editors: [vscode]\n")
	if out, err := editorsSelectionRun(t, root, "generate"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	commented := map[string]string{}
	for _, name := range []string{"settings.json", "extensions.json", "tasks.json"} {
		full := filepath.Join(root, ".vscode", name)
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		commented[name] = commentedGenerated(t, string(data))
		if err := os.WriteFile(full, []byte(commented[name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := editorsSelectionRun(t, root, "verify"); err != nil {
		t.Fatalf("verify refused commented files holding every managed value: %v\n%s", err, out)
	}
	out, err := editorsSelectionRun(t, root, "generate")
	if err != nil {
		t.Fatalf("generate refused commented files holding every managed value: %v\n%s", err, out)
	}
	mustContain(t, out, "[PRESENT] [vscode] .vscode/settings.json", "0 merged")
	for name, want := range commented {
		if got, err := os.ReadFile(filepath.Join(root, ".vscode", name)); err != nil || string(got) != want {
			t.Errorf("generate changed the commented %s: %v\n%s", name, err, got)
		}
	}

	lacking := "{\n  // adopter note\n  \"adopter.custom\": \"keep\"\n}\n"
	settings := filepath.Join(root, ".vscode", "settings.json")
	if err := os.WriteFile(settings, []byte(lacking), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := editorsSelectionRun(t, root, "verify"); err == nil || !strings.Contains(err.Error(), ".vscode/settings.json is missing managed standards policy") {
		t.Errorf("verify of a commented file lacking managed values: %v", err)
	}
	_, err = editorsSelectionRun(t, root, "generate")
	if err == nil {
		t.Fatal("generate rewrote a commented settings.json")
	}
	for _, want := range []string{"cannot safely merge existing .vscode/settings.json", "comments or trailing commas", "would lose", "by hand"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}
	if got, err := os.ReadFile(settings); err != nil || string(got) != lacking {
		t.Errorf("a refused generate changed the commented settings.json: %v\n%s", err, got)
	}
}
