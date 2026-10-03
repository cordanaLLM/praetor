//go:build unix

package contextopt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestLockSnapshotDirectoryReleasesAcrossExecFork(t *testing.T) {
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

	cmdName := "true"
	var cmdArgs []string
	if _, err := exec.LookPath(cmdName); err != nil {
		cmdName = "sh"
		cmdArgs = []string{"-c", ":"}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var stop atomic.Bool

	wg.Add(1)
	go func() {
		defer wg.Done()
		const maxExecs = 5000
		for j := 0; j < maxExecs && !stop.Load() && ctx.Err() == nil; j++ {
			cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
			if err := cmd.Run(); err != nil && ctx.Err() == nil {
				t.Logf("exec child %s finished with error: %v", cmdName, err)
			}
		}
	}()
	// A t.Fatalf below must not leave the exec goroutine running past the test.
	defer func() {
		stop.Store(true)
		wg.Wait()
	}()

	const lockCycles = 2000
	var failures int
	for i := 0; i < lockCycles; i++ {
		unlock, err := lockSnapshotDirectory(root)
		if err != nil {
			failures++
			continue
		}
		if err := unlock(); err != nil {
			t.Fatalf("cycle %d failed to release directory lock: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()

	if failures != 0 {
		t.Fatalf("spurious lock failures across exec fork: %d of %d failed", failures, lockCycles)
	}
}

func TestLockSnapshotDirectoryReleaseAfterReleaseReported(t *testing.T) {
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
	if err := unlock(); err != nil {
		t.Fatalf("first unlock failed: %v", err)
	}
	if err := unlock(); err == nil {
		t.Fatal("second release should report an error on closed file descriptor")
	}
}

// Boundary: a stale release of lock A must not unlock lock B. The case relies on POSIX
// handing out the lowest free descriptor, so B's directory reuses the number A's release
// closed; nothing else in this test opens a file in between, and the package's tests do not
// run in parallel. Against a release that unlocks the raw descriptor number, lock C succeeds.
func TestLockSnapshotDirectoryDoubleReleaseDoesNotDropReusedFDLock(t *testing.T) {
	dirA := t.TempDir()
	rootA, err := OpenDirectory(t.Context(), dirA)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rootA.Close(); err != nil {
			t.Error(err)
		}
	}()

	dirB := t.TempDir()
	rootB, err := OpenDirectory(t.Context(), dirB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rootB.Close(); err != nil {
			t.Error(err)
		}
	}()

	rootC, err := OpenDirectory(t.Context(), dirB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rootC.Close(); err != nil {
			t.Error(err)
		}
	}()

	releaseA, err := lockSnapshotDirectory(rootA)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseA(); err != nil {
		t.Fatalf("first release A failed: %v", err)
	}

	releaseB, err := lockSnapshotDirectory(rootB)
	if err != nil {
		t.Fatal(err)
	}
	var releasedB bool
	defer func() {
		if !releasedB {
			if err := releaseB(); err != nil {
				t.Errorf("cleanup release B failed: %v", err)
			}
		}
	}()

	// Second release on A must not drop B's lock when the fd number is reused.
	if err := releaseA(); err == nil {
		t.Fatal("second release A should report an error on closed file")
	}

	releaseC, err := lockSnapshotDirectory(rootC)
	if err == nil {
		if err := releaseC(); err != nil {
			t.Errorf("unexpected release C failed: %v", err)
		}
		t.Fatal("lock C succeeded while B held the lock")
	}

	if err := releaseB(); err != nil {
		t.Fatalf("release B failed: %v", err)
	}
	releasedB = true

	releaseC2, err := lockSnapshotDirectory(rootC)
	if err != nil {
		t.Fatalf("lock C failed after B released: %v", err)
	}
	if err := releaseC2(); err != nil {
		t.Fatalf("release C failed: %v", err)
	}
}
