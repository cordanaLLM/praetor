package editor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRejectsTraversalBeforeAnyOutput(t *testing.T) {
	root := t.TempDir()
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: "first.json", Content: "{}"}, {Path: "../outside.json", Content: "{}"}}}
	if err := Write(set, root); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := os.Stat(filepath.Join(root, "first.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote before validation: %v", err)
	}
	if err := Verify(set, root); err == nil {
		t.Fatal("verified traversal")
	}
}

func TestWriteRejectsSymlinkAndPreservesPrivateMode(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: "config.json", Content: "{}"}}}
	if err := Write(set, root); err == nil {
		t.Fatal("followed output symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private permissions widened: %o", info.Mode().Perm())
	}
	actual, err := os.ReadFile(outside)
	if err != nil || string(actual) != "unchanged" {
		t.Fatalf("outside changed: %s %v", actual, err)
	}
}
