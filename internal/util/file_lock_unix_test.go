// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package util_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const testLockName = "test fixture"

func lockRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root, dir
}

// acquire takes the fixture lock and fails the test unless it was free.
func acquire(t *testing.T, root *os.Root, path string) func() error {
	t.Helper()
	release, busy, err := util.LockPrivateFile(root, path, os.O_RDWR|os.O_CREATE, testLockName)
	if err != nil || busy {
		t.Fatalf("lock %s: busy=%v err=%v", path, busy, err)
	}
	return release
}

// requireBusy fails the test unless another holder has the fixture lock.
func requireBusy(t *testing.T, root *os.Root, path string) {
	t.Helper()
	release, busy, err := util.LockPrivateFile(root, path, os.O_RDWR, testLockName)
	if err != nil || !busy || release != nil {
		if release != nil {
			if err := release(); err != nil {
				t.Error(err)
			}
		}
		t.Fatalf("second holder of %s: busy=%v err=%v", path, busy, err)
	}
}

func TestLockExclusiveRefusesSecondHolderUntilReleased(t *testing.T) {
	root, _ := lockRoot(t)
	release := acquire(t, root, "ledger.lock")
	requireBusy(t, root, "ledger.lock")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := acquire(t, root, "ledger.lock")(); err != nil {
		t.Fatal(err)
	}
}

// A subprocess forked by another goroutine holds copies of every descriptor until its exec.
// The copy made here stands in for it deterministically: against a release that only closes
// the file, the copy keeps the lock and the second acquire reports busy.
func TestLockExclusiveReleaseDropsLockHeldByForkedCopy(t *testing.T) {
	root, dir := lockRoot(t)
	release := acquire(t, root, "ledger.lock")
	testsupport.CopyDescriptorsOf(t, filepath.Join(dir, "ledger.lock"))
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := acquire(t, root, "ledger.lock")(); err != nil {
		t.Fatal(err)
	}
}

// Bounded lock cycles while another goroutine starts processes: no cycle may find the lock
// busy. Against a close-only release, the descriptors a child holds between fork and exec
// keep the lock and cycles fail. The cycles run until the processes have started, so the two
// overlap however fast either side is.
func TestLockExclusiveReleasesAcrossConcurrentProcessStarts(t *testing.T) {
	root, _ := lockRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	const processStarts, maxCycles = 200, 1_000_000
	var started atomic.Int64
	var wg sync.WaitGroup
	wg.Go(func() { startProcesses(ctx, t, &started, processStarts) })
	// A t.Fatalf below must not leave the process goroutine running past the test.
	defer func() {
		cancel()
		wg.Wait()
	}()
	cycles, busyCycles := 0, 0
	for ; cycles < maxCycles && started.Load() < processStarts && ctx.Err() == nil; cycles++ {
		release, busy, err := util.LockPrivateFile(root, "cycle.lock", os.O_RDWR|os.O_CREATE, testLockName)
		if err != nil {
			t.Fatalf("cycle %d: %v", cycles, err)
		}
		if busy {
			busyCycles++
			continue
		}
		if err := release(); err != nil {
			t.Fatalf("cycle %d failed to release: %v", cycles, err)
		}
	}
	if busyCycles != 0 {
		t.Fatalf("spurious busy locks across %d process starts: %d of %d cycles", started.Load(), busyCycles, cycles)
	}
	t.Logf("%d lock cycles across %d process starts", cycles, started.Load())
}

func startProcesses(ctx context.Context, t *testing.T, started *atomic.Int64, limit int64) {
	name, args := "true", []string(nil)
	if _, err := exec.LookPath(name); err != nil {
		name, args = "sh", []string{"-c", ":"}
	}
	for started.Load() < limit && ctx.Err() == nil {
		// #nosec G204 -- name and args are the fixed no-op commands chosen above.
		if err := exec.CommandContext(ctx, name, args...).Run(); err != nil && ctx.Err() == nil {
			t.Logf("process %s finished with error: %v", name, err)
		}
		started.Add(1)
	}
}

// Boundary: a second release changes nothing and says so. It must not unlock lock B, whose
// file reuses the descriptor number A's release closed (POSIX hands out the lowest free
// number, and nothing else in this test opens a file in between).
func TestLockExclusiveSecondReleaseIsHarmlessAndReported(t *testing.T) {
	root, _ := lockRoot(t)
	releaseA := acquire(t, root, "a.lock")
	if err := releaseA(); err != nil {
		t.Fatal(err)
	}
	releaseB := acquire(t, root, "b.lock")
	defer func() {
		if err := releaseB(); err != nil {
			t.Error(err)
		}
	}()
	err := releaseA()
	if !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "release "+testLockName+" lock: already released") {
		t.Fatalf("second release: %v", err)
	}
	requireBusy(t, root, "b.lock")
}

func TestLockExclusiveRefusesUnusableFile(t *testing.T) {
	if _, _, err := util.LockExclusive(nil, testLockName); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("nil file: %v", err)
	}
	root, _ := lockRoot(t)
	file, err := root.Create("closed.lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	release, busy, err := util.LockExclusive(file, testLockName)
	if err == nil || busy || release != nil || !strings.Contains(err.Error(), "lock "+testLockName) {
		t.Errorf("closed file: release=%v busy=%v err=%v", release != nil, busy, err)
	}
}

func TestLockPrivateFileRefusesUnsafeLockFiles(t *testing.T) {
	root, dir := lockRoot(t)
	if err := os.WriteFile(filepath.Join(dir, "shared.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "shared.lock"), 0o640); err != nil {
		t.Fatal(err)
	}
	// The link names a private regular file, so only the refusal to follow can stop it.
	if err := os.WriteFile(filepath.Join(dir, "target.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.lock", filepath.Join(dir, "link.lock")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shared.lock", "link.lock", "fifo.lock"} {
		release, busy, err := util.LockPrivateFile(root, name, os.O_RDWR, testLockName)
		if err == nil || busy || release != nil || !strings.Contains(err.Error(), testLockName) {
			t.Errorf("%s: release=%v busy=%v err=%v", name, release != nil, busy, err)
		}
	}
}

func TestLockPrivateFileReportsMissingLockAndCreatesPrivately(t *testing.T) {
	root, dir := lockRoot(t)
	if _, _, err := util.LockPrivateFile(nil, "absent.lock", os.O_RDWR, testLockName); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("nil root: %v", err)
	}
	if _, _, err := util.LockPrivateFile(root, "absent.lock", os.O_RDONLY, testLockName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent lock without create: %v", err)
	}
	if err := acquire(t, root, "absent.lock")(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "absent.lock"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("created lock: %v %v", info, err)
	}
	// Boundary: a read-only open still takes the exclusive lock.
	release, busy, err := util.LockPrivateFile(root, "absent.lock", os.O_RDONLY, testLockName)
	if err != nil || busy {
		t.Fatalf("read-only lock: busy=%v err=%v", busy, err)
	}
	requireBusy(t, root, "absent.lock")
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
