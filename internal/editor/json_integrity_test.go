package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorJSONMergePreservesNumbersAndRejectsDuplicateKeys(t *testing.T) {
	desired := `{"files.insertFinalNewline":true}`
	root := writeFixture(t, map[string]string{
		".vscode/settings.json": `{"editor.fontSize":9007199254740993}`,
	})
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Content: desired}}}
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	merged := mustRead(t, filepath.Join(root, ".vscode", "settings.json"))
	if !strings.Contains(merged, "9007199254740993") {
		t.Fatalf("JSON merge changed an unrelated integer: %s", merged)
	}

	duplicate := `{"editor.fontSize":41,"editor.fontSize":42}`
	writeTestFile(t, root, ".vscode/settings.json", duplicate)
	if err := Write(set, root); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ambiguous existing JSON was accepted: %v", err)
	}
	if after := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); after != duplicate {
		t.Fatalf("duplicate-key rejection changed existing JSON: %s", after)
	}
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeTestFile(t, root, path, content)
	}
	return root
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
