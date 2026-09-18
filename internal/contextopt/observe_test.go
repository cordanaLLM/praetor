package contextopt

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveRootSnapshot(t *testing.T) {
	dir := t.TempDir()
	root, err := OpenDirectory(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	if data, exists, err := ObserveRootSnapshot(t.Context(), root, "absent.md"); err != nil || exists || data != nil {
		t.Fatalf("absence must read as not existing: %q %v %v", data, exists, err)
	}
	boundary := bytes.Repeat([]byte("a"), MaxSourceBytes)
	if err := os.WriteFile(filepath.Join(dir, "present.md"), boundary, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, exists, err := ObserveRootSnapshot(t.Context(), root, "present.md"); err != nil || !exists || !bytes.Equal(data, boundary) {
		t.Fatalf("exact-bound file not observed: %d bytes, %v, %v", len(data), exists, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "present.md"), append(boundary, 'a'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"present.md", "directory", "../escape.md"} {
		if _, _, err := ObserveRootSnapshot(t.Context(), root, name); err == nil {
			t.Fatalf("%s observed without error", name)
		}
	}
	var missing context.Context
	if _, _, err := ObserveRootSnapshot(missing, root, "absent.md"); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, _, err := ObserveRootSnapshot(t.Context(), nil, "absent.md"); err == nil {
		t.Fatal("nil directory accepted")
	}
}
