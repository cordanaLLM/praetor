package contextopt

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadBinarySnapshotExactBytesAndBoundary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset.bin")
	content := bytes.Repeat([]byte{0xff, 0}, MaxSourceBytes/2)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBinarySnapshot(t.Context(), path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("binary boundary changed: %v", err)
	}
	if _, err := ReadSnapshot(t.Context(), path); err == nil {
		t.Fatal("text reader accepted binary input")
	}
	if err := os.WriteFile(path, append(content, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBinarySnapshot(t.Context(), path); err == nil {
		t.Fatal("binary reader accepted overflow")
	}
}

func TestReadBinarySnapshotRejectsInvalidPathsAndContext(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "missing")
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, link, root} {
		if _, err := ReadBinarySnapshot(t.Context(), path); err == nil {
			t.Fatalf("invalid path accepted: %s", path)
		}
	}
	var missingContext context.Context
	if _, err := ReadBinarySnapshot(missingContext, target); err == nil {
		t.Fatal("nil context accepted")
	}
}
