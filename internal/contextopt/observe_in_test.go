package contextopt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Positive: ObserveSnapshotIn reads a regular file below root and reports it as existing.
func TestObserveSnapshotIn_Positive_ReadsRegularFile(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(".claude", "settings.json")
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, exists, err := ObserveSnapshotIn(t.Context(), root, rel)
	if err != nil || !exists || string(data) != "{}\n" {
		t.Fatalf("ObserveSnapshotIn = %q, %v, %v", data, exists, err)
	}
}

// Negative: a symlinked leaf and a symlinked directory component are refused, even when the
// link stays inside root, and a missing context is an error rather than a panic.
func TestObserveSnapshotIn_Negative_RefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared", filepath.Join(root, ".gemini")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join("shared", "settings.json"), filepath.Join(root, "settings.json")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"settings.json", filepath.Join(".gemini", "settings.json")} {
		if _, _, err := ObserveSnapshotIn(t.Context(), root, rel); err == nil {
			t.Errorf("%s: symlink followed", rel)
		}
	}
	var nilContext context.Context
	if _, _, err := ObserveSnapshotIn(nilContext, root, "settings.json"); err == nil {
		t.Error("nil context accepted")
	}
}

// Boundary: an absent directory and an absent file both read as not existing, without creating
// anything, and a file directly below root is found through the "." parent.
func TestObserveSnapshotIn_Boundary_AbsenceCreatesNothing(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{filepath.Join(".codex", "hooks.json"), "hooks.json"} {
		data, exists, err := ObserveSnapshotIn(t.Context(), root, rel)
		if err != nil || exists || data != nil {
			t.Fatalf("%s: ObserveSnapshotIn = %q, %v, %v", rel, data, exists, err)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("absent observation created %v (err %v)", entries, err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if data, exists, err := ObserveSnapshotIn(t.Context(), root, "hooks.json"); err != nil || !exists || len(data) != 0 {
		t.Fatalf("empty root file = %q, %v, %v", data, exists, err)
	}
}
