//go:build unix

package contextopt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacementRefusesBusyDirectoryWithoutStaging(t *testing.T) {
	dir := t.TempDir()
	root, err := OpenDirectory(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	unlock, err := lockSnapshotDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	if err := ReplaceSnapshot(t.Context(), filepath.Join(dir, "new"), []byte("content"), ReplaceOptions{Mode: 0o600}); err == nil {
		t.Fatal("write accepted while another writer holds directory lock")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("busy lock left staged artifacts: %v, %v", entries, err)
	}
}

func TestFailedPublicationRetainsPrivateStagedText(t *testing.T) {
	dir := t.TempDir()
	root, err := OpenDirectory(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	file, err := stageSnapshot(t.Context(), root, "private.pending", []byte("recover this content"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	// The caller observed a file which disappears before publication.
	err = publishSnapshot(t.Context(), root, "missing", "private.pending", file, 0o644,
		ReplaceOptions{Exists: true, Expected: []byte("previous"), Mode: 0o644})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source should fail before publishing: %v", err)
	}
	content, err := ReadRootSnapshot(t.Context(), root, "private.pending")
	if err != nil || string(content) != "recover this content" {
		t.Fatalf("failed publication lost staged content: %q, %v", content, err)
	}
	info, err := root.Stat("private.pending")
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("failed stage is not private: %v, %v", info, err)
	}
}
