package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-776: editor settings followed the archetype, so a repository with no Go at all received
// go.useLanguageServer, a Go formatter and $go problem matchers.
func TestSynthesize_Positive_NoGoSettingsWithoutGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.ts"), []byte("export const a = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := Synthesize(Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}, Archetype: "pages-site"})
	if err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	for _, f := range set.Files {
		if strings.Contains(f.Content, "go.useLanguageServer") {
			t.Errorf("%s asserts a Go language server in a repository with no Go", f.Path)
		}
		if strings.Contains(f.Content, `"$go"`) {
			t.Errorf("%s asserts a $go problem matcher in a repository with no Go", f.Path)
		}
	}
}

// Negative: a repository that does contain Go still gets its Go settings.
func TestSynthesize_Negative_KeepsGoSettingsWhenGoIsPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := Synthesize(Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}, Archetype: "app-service"})
	if err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	var sawGo bool
	for _, f := range set.Files {
		if strings.Contains(f.Content, "go.useLanguageServer") {
			sawGo = true
		}
	}
	if !sawGo {
		t.Error("a Go repository lost its Go settings")
	}
}
