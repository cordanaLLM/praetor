//go:build unix

package contextopt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
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

// The snapshot directory lock goes through util.LockExclusive, whose tests cover concurrent
// process starts and repeated releases. Here: a second writer is refused with this lock's
// message, a descriptor copy such as a forked child holds does not keep the lock past its
// release (it does against a close-only release), and a second release is reported.
func TestLockSnapshotDirectoryReleaseDropsLockHeldByForkedCopy(t *testing.T) {
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
	release, err := lockSnapshotDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockSnapshotDirectory(root); err == nil || second != nil || !strings.Contains(err.Error(), "snapshot directory busy") {
		t.Fatalf("second writer: %v", err)
	}
	testsupport.CopyDescriptorsOf(t, dir)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := lockSnapshotDirectory(root)
	if err != nil {
		t.Fatalf("lock still held by a descriptor copy after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release not reported: %v", err)
	}
}
