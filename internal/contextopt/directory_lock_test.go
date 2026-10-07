// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #820: the snapshot directory lock refused the second writer at the first contention.
// These tests pin what replaced that: the writers of one process take turns, another
// process's lock is waited for within a budget, and a lock held past the budget fails naming
// the holder and the budget. Writers of other processes are stood in for by the platform lock
// taken directly (tryLockSnapshotDirectory), which bypasses this process's turns exactly as
// another process does; directory_lock_process_test.go repeats the cases with a real one.

// pinnedDirectory opens a fresh temporary directory and closes it when the test ends.
func pinnedDirectory(t *testing.T) (*os.Root, string) {
	t.Helper()
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
	return root, dir
}

// holdPlatformLock takes the platform lock on root as another process would and returns its
// release, which the test's cleanup also runs.
func holdPlatformLock(t *testing.T, root *os.Root) func() {
	t.Helper()
	release, busy, err := tryLockSnapshotDirectory(root)
	if err != nil || busy {
		t.Fatalf("hold platform lock: busy=%v err=%v", busy, err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// budgetContext returns the test's context carrying budget.
func budgetContext(t *testing.T, budget time.Duration) context.Context {
	t.Helper()
	ctx, err := WithDirectoryLockBudget(t.Context(), budget)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// mustLock takes LockDirectory on root and fails the test otherwise.
func mustLock(t *testing.T, ctx context.Context, root *os.Root) func() error {
	t.Helper()
	release, err := LockDirectory(ctx, root)
	if err != nil {
		t.Fatalf("lock %s: %v", root.Name(), err)
	}
	return release
}

// requireHolderNamed fails unless err names this process as the platform lock's holder where
// the platform reports one, and says it cannot where it does not.
func requireHolderNamed(t *testing.T, err error, pid int) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "windows":
		if !strings.Contains(err.Error(), "process "+strconv.Itoa(pid)) {
			t.Fatalf("busy error does not name holder %d: %v", pid, err)
		}
	default:
		if !strings.Contains(err.Error(), "does not report") {
			t.Fatalf("busy error neither names the holder nor says it cannot: %v", err)
		}
	}
}

// Positive: a second writer of this process waits for the first instead of failing, and
// publishes once the first releases. Before #820 it failed at once with "snapshot directory
// busy".
func TestLockDirectory_Positive_WritersOfOneProcessTakeTurns(t *testing.T) {
	root, dir := pinnedDirectory(t)
	release := mustLock(t, t.Context(), root)
	done := make(chan error, 1)
	go func() { done <- WriteSnapshotIn(t.Context(), dir, "second", []byte("second writer\n"), 0o600) }()
	select {
	case err := <-done:
		t.Fatalf("second writer returned while the first held the directory: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second writer after the first released: %v", err)
	}
	if got := readFileOrEmpty(t, filepath.Join(dir, "second")); got != "second writer\n" {
		t.Fatalf("second writer published %q", got)
	}
}

// Positive: many writers of one process publishing into one directory at once all succeed.
// Adoption writes its targets one after another today; the in-process lock keeps a future
// concurrent writer from failing on its own sibling.
func TestLockDirectory_Positive_ConcurrentWritersAllPublish(t *testing.T) {
	_, dir := pinnedDirectory(t)
	const writers = 16
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			errs <- WriteSnapshotIn(t.Context(), dir, fmt.Sprintf("file-%02d", i), []byte("content\n"), 0o600)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent writer refused: %v", err)
		}
	}
	if left := turnsInUse(); left != 0 {
		t.Fatalf("turns left behind after every writer released: %d", left)
	}
}

// turnsInUse counts the directories some writer of this process holds or waits for.
func turnsInUse() int {
	processDirectoryTurns.mu.Lock()
	defer processDirectoryTurns.mu.Unlock()
	return len(processDirectoryTurns.entries)
}

// Positive: turns are per directory; a writer holding one does not delay another.
func TestLockDirectory_Positive_OtherDirectoriesDoNotWait(t *testing.T) {
	first, _ := pinnedDirectory(t)
	second, _ := pinnedDirectory(t)
	held := mustLock(t, t.Context(), first)
	defer func() {
		if err := held(); err != nil {
			t.Error(err)
		}
	}()
	if err := mustLock(t, budgetContext(t, 0), second)(); err != nil {
		t.Fatal(err)
	}
}

// Positive: another process's lock released within the budget lets the writer finish.
func TestLockDirectory_Positive_WaitsForOtherProcessWithinBudget(t *testing.T) {
	root, _ := pinnedDirectory(t)
	stop := holdPlatformLock(t, root)
	time.AfterFunc(100*time.Millisecond, stop)
	started := time.Now()
	if err := mustLock(t, budgetContext(t, 20*time.Second), root)(); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited < 50*time.Millisecond {
		t.Fatalf("lock taken after %s while another holder kept it for 100ms", waited)
	}
}

// Negative: a lock another process holds past the budget fails the writer after the budget,
// naming the holder and the budget, and leaves this process's turn free.
func TestLockDirectory_Negative_HeldPastBudgetNamesHolderAndBudget(t *testing.T) {
	root, _ := pinnedDirectory(t)
	stop := holdPlatformLock(t, root)
	started := time.Now()
	release, err := LockDirectory(budgetContext(t, 150*time.Millisecond), root)
	if err == nil || release != nil {
		t.Fatal("lock taken while another holder kept it past the budget")
	}
	if waited := time.Since(started); waited < 150*time.Millisecond {
		t.Fatalf("gave up after %s, before the 150ms budget", waited)
	}
	for _, want := range []string{"snapshot directory busy: ", "held the lock on " + root.Name(), "past the 150ms wait budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("busy error lacks %q: %v", want, err)
		}
	}
	requireHolderNamed(t, err, os.Getpid())
	stop()
	if err := mustLock(t, budgetContext(t, 0), root)(); err != nil {
		t.Fatalf("turn or lock still held after a failed wait: %v", err)
	}
}

// Negative: a writer of this process waits for the one ahead of it only until its context
// ends, and then names that writer.
func TestLockDirectory_Negative_TurnWaitEndsWithContext(t *testing.T) {
	root, _ := pinnedDirectory(t)
	held := mustLock(t, t.Context(), root)
	defer func() {
		if err := held(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := LockDirectory(ctx, root)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("turn wait did not end with its context: %v", err)
	}
	want := fmt.Sprintf("another writer in this process (pid %d)", os.Getpid())
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("turn wait error lacks %q: %v", want, err)
	}
	_, err = LockDirectory(budgetContext(t, 0), root)
	if err == nil || !strings.Contains(err.Error(), want+" held the lock on "+root.Name()+" past the 0s wait budget") {
		t.Fatalf("zero budget against a writer of this process: %v", err)
	}
}

// Negative: a second release changes nothing, reports os.ErrClosed and frees no turn it
// does not hold.
func TestLockDirectory_Negative_SecondReleaseIsReported(t *testing.T) {
	root, _ := pinnedDirectory(t)
	release := mustLock(t, t.Context(), root)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	held := mustLock(t, t.Context(), root)
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release: %v", err)
	}
	if _, err := LockDirectory(budgetContext(t, 0), root); err == nil {
		t.Fatal("second release of an earlier lock freed the lock held now")
	}
	if err := held(); err != nil {
		t.Fatal(err)
	}
}

// Boundary: with a zero budget a free directory is still locked and a held one fails on the
// first attempt.
func TestLockDirectory_Boundary_ZeroBudgetMakesOneAttempt(t *testing.T) {
	root, _ := pinnedDirectory(t)
	if err := mustLock(t, budgetContext(t, 0), root)(); err != nil {
		t.Fatal(err)
	}
	holdPlatformLock(t, root)
	started := time.Now()
	_, err := LockDirectory(budgetContext(t, 0), root)
	if err == nil || !strings.Contains(err.Error(), "past the 0s wait budget") {
		t.Fatalf("zero budget against a held lock: %v", err)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Fatalf("zero budget waited %s", waited)
	}
}

// Boundary: a lock released just as the budget ends is still taken, by the last attempt the
// budget's end makes; held through that attempt, the writer fails.
func TestLockDirectory_Boundary_LastAttemptAtBudgetEnd(t *testing.T) {
	for name, releaseAtEnd := range map[string]bool{"released at the end": true, "held through the end": false} {
		t.Run(name, func(t *testing.T) {
			root, _ := pinnedDirectory(t)
			stop := holdPlatformLock(t, root)
			pause := lockPause(func(ctx context.Context, delay time.Duration) bool {
				if sleepUntil(ctx, delay) {
					return true
				}
				if releaseAtEnd {
					stop()
				}
				return false
			})
			ctx := context.WithValue(budgetContext(t, 50*time.Millisecond), lockPauseKey{}, pause)
			release, err := LockDirectory(ctx, root)
			if releaseAtEnd != (err == nil) {
				t.Fatalf("release at the end=%v: %v", releaseAtEnd, err)
			}
			if release != nil {
				if err := release(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWithDirectoryLockBudget_RefusesAndBounds(t *testing.T) {
	var absent context.Context
	if _, err := WithDirectoryLockBudget(absent, time.Second); err == nil {
		t.Error("nil context accepted")
	}
	if _, err := WithDirectoryLockBudget(t.Context(), -time.Nanosecond); err == nil {
		t.Error("negative budget accepted")
	}
	for budget, want := range map[time.Duration]time.Duration{
		0:                         0,
		MaxDuration:               MaxDuration,
		MaxDuration + time.Second: MaxDuration,
	} {
		wait := directoryLockWait(budgetContext(t, budget))
		if !wait.explicit || wait.budget != want {
			t.Errorf("budget %s: got %+v, want %s", budget, wait, want)
		}
	}
	if wait := directoryLockWait(t.Context()); wait.explicit || wait.budget != DirectoryLockBudget {
		t.Errorf("default wait: %+v", wait)
	}
}

func TestLockDirectory_Negative_RefusesMissingInputs(t *testing.T) {
	root, _ := pinnedDirectory(t)
	var absent context.Context
	if _, err := LockDirectory(absent, root); err == nil {
		t.Error("nil context accepted")
	}
	if _, err := LockDirectory(t.Context(), nil); err == nil {
		t.Error("nil root accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LockDirectory(ctx, root); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v", err)
	}
}

func TestDescribeLockHolders(t *testing.T) {
	self := os.Getpid()
	for _, tc := range []struct {
		pids []int
		want string
	}{
		{nil, "another process (why)"},
		{[]int{self + 1}, fmt.Sprintf("process %d", self+1)},
		{[]int{self, self}, fmt.Sprintf("process %d (this process)", self)},
		{[]int{self + 1, self + 2, self + 3}, fmt.Sprintf("process %d, process %d and process %d", self+1, self+2, self+3)},
	} {
		if got := describeLockHolders(tc.pids, "why"); got != tc.want {
			t.Errorf("describeLockHolders(%v) = %q, want %q", tc.pids, got, tc.want)
		}
	}
}
