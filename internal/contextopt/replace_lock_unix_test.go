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

// A write refused because another process holds the directory past the budget stages nothing.
func TestReplacementRefusesBusyDirectoryWithoutStaging(t *testing.T) {
	root, dir := pinnedDirectory(t)
	holdPlatformLock(t, root)
	err := ReplaceSnapshot(budgetContext(t, 0), filepath.Join(dir, "new"), []byte("content"), ReplaceOptions{Mode: 0o600})
	if err == nil || !strings.Contains(err.Error(), "snapshot directory busy") {
		t.Fatalf("write while another writer holds the directory past the budget: %v", err)
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

// The snapshot directory's platform lock goes through util.LockExclusive, whose tests cover
// concurrent process starts and repeated releases. Here: a second open file description is
// reported busy, a descriptor copy such as a forked child holds does not keep the lock past
// its release (it does against a close-only release), and a second release is reported.
func TestLockSnapshotDirectoryReleaseDropsLockHeldByForkedCopy(t *testing.T) {
	root, dir := pinnedDirectory(t)
	release, busy, err := tryLockSnapshotDirectory(root)
	if err != nil || busy {
		t.Fatalf("first writer: busy=%v err=%v", busy, err)
	}
	if second, busy, err := tryLockSnapshotDirectory(root); err != nil || !busy || second != nil {
		t.Fatalf("second writer: busy=%v err=%v", busy, err)
	}
	testsupport.CopyDescriptorsOf(t, dir)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, busy, err := tryLockSnapshotDirectory(root)
	if err != nil || busy {
		t.Fatalf("lock still held by a descriptor copy after release: busy=%v err=%v", busy, err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release not reported: %v", err)
	}
}
