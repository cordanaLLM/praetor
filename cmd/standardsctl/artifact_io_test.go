package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestCommandArtifactPreservesPrivateModeAndRefusesLink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "artifact.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandArtifact(t.Context(), path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if util.ModeIsProtection() && info.Mode().Perm() != 0600 {
		t.Fatalf("existing mode widened to %#o", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatalf("artifact not updated: %q", content)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandArtifact(t.Context(), link, []byte("escaped"), 0600); err == nil {
		t.Fatal("symlink accepted")
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatal("symlink target changed")
	}
}
