package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestCorrelationStorePositive(t *testing.T) {
	store, err := newCorrelationStore(t.Context(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := config.Resolution{Register: config.TextRegisterInternal, MaxTokens: 512, Source: "tasks.ci_debugging"}
	if err := store.reserve(t.Context(), "claude", "session", "tool", want); err != nil {
		t.Fatal(err)
	}
	if err := store.promote(t.Context(), "claude", "session", "tool", "agent"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.active(t.Context(), "claude", "session", "agent"); err != nil || got.Resolution != want {
		t.Fatalf("correlation = %+v, %v", got, err)
	}
	if err := store.markHandbackValidated(t.Context(), "claude", "session", "agent", "handback", "report"); err != nil {
		t.Fatal(err)
	}
	if err := store.markHandbackDelivered(t.Context(), "claude", "session", "agent", "handback", "report"); err != nil {
		t.Fatal(err)
	}
	delivered, err := correlationName("delivered", "claude", "session", "agent")
	if err != nil {
		t.Fatal(err)
	}
	active, err := correlationName("active", "claude", "session", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.active(t.Context(), "claude", "session", "agent"); err != nil || !got.HandbackDelivered {
		t.Fatalf("delivered correlation = %+v, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(store.dir, active)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active correlation remained after delivery: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.dir, delivered)); err != nil {
		t.Fatalf("delivered correlation missing: %v", err)
	}
	if err := store.complete(t.Context(), "claude", "session", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.active(t.Context(), "claude", "session", "agent"); err == nil {
		t.Fatal("completed correlation remained readable")
	}
}

func TestCorrelationStoreNegative(t *testing.T) {
	if _, err := newCorrelationStore(t.Context(), "", "relative"); err == nil {
		t.Fatal("relative override accepted")
	}
	store, err := newCorrelationStore(t.Context(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	for name, call := range map[string]func() error{
		"empty id": func() error { return store.reserve(t.Context(), "claude", "", "tool", resolution) },
		"long id": func() error {
			return store.reserve(t.Context(), "claude", "session", string(make([]byte, MaxCorrelationIDBytes+1)), resolution)
		},
		"missing bind": func() error { return store.promote(t.Context(), "claude", "session", "missing", "agent") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("invalid correlation accepted")
			}
		})
	}
}

// TestCorrelationStoreEvictsOldestAtCapacity: below the cap nothing is evicted; at the cap a
// reservation evicts exactly the least recently written row, never a newer one, and a
// conflicting reservation evicts nothing.
func TestCorrelationStoreEvictsOldestAtCapacity(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	for index := 0; index < MaxCorrelationEntries; index++ {
		if err := store.reserve(t.Context(), "claude", "session", fmt.Sprintf("tool-%03d", index), resolution); err != nil {
			t.Fatalf("reserve %d: %v", index, err)
		}
	}
	requireStoreRows(t, dir, MaxCorrelationEntries)
	oldest := correlationPath(t, dir, "pending", "tool-064")
	older := time.Now().Add(-time.Minute)
	if err := os.Chtimes(oldest, older, older); err != nil {
		t.Fatal(err)
	}
	if err := store.reserve(t.Context(), "claude", "session", "tool-010", resolution); err == nil {
		t.Fatal("conflicting reservation accepted")
	}
	if _, err := os.Lstat(oldest); err != nil {
		t.Fatalf("a refused reservation evicted a row: %v", err)
	}
	if err := store.reserve(t.Context(), "claude", "session", "overflow", resolution); err != nil {
		t.Fatalf("reservation at capacity refused: %v", err)
	}
	requireStoreRows(t, dir, MaxCorrelationEntries)
	if _, err := os.Lstat(oldest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest row survived eviction: %v", err)
	}
	for _, kept := range []string{"tool-000", "tool-063", "tool-065", "tool-127", "overflow"} {
		if _, err := os.Lstat(correlationPath(t, dir, "pending", kept)); err != nil {
			t.Fatalf("%s was evicted instead of the oldest row: %v", kept, err)
		}
	}
}

func TestCorrelationStoreReclaimsExpiredRowsBeforeEvicting(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	for index := 0; index < MaxCorrelationEntries; index++ {
		if err := store.reserve(t.Context(), "claude", "session", fmt.Sprintf("tool-%03d", index), resolution); err != nil {
			t.Fatalf("reserve %d: %v", index, err)
		}
	}
	stale := correlationPath(t, dir, "pending", "tool-100")
	old := time.Now().Add(-correlationActiveTTL - time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := store.reserve(t.Context(), "claude", "session", "replacement", resolution); err != nil {
		t.Fatalf("stale slot was not reclaimed: %v", err)
	}
	requireStoreRows(t, dir, MaxCorrelationEntries)
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired row survived: %v", err)
	}
	if _, err := os.Lstat(correlationPath(t, dir, "pending", "tool-000")); err != nil {
		t.Fatalf("a live row was evicted although an expired one freed the slot: %v", err)
	}
}

// TestCorrelationStoreSweepsAbandonedTempFiles: a writer killed mid-write leaves a
// util.WriteFileAtomic temporary file; one older than the lock lease is removed, a fresh one
// (a writer that may still hold the lock) is kept, and a foreign file is never touched.
func TestCorrelationStoreSweepsAbandonedTempFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := filepath.Join(dir, util.AtomicTempPrefix+"pending-abandoned.json-1")
	fresh := filepath.Join(dir, util.AtomicTempPrefix+"pending-fresh.json-2")
	foreign := filepath.Join(dir, "notes.txt")
	for _, path := range []string{abandoned, fresh, foreign} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-correlationLockTTL - time.Minute)
	for _, path := range []string{abandoned, foreign} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	if err := store.reserve(t.Context(), "claude", "session", "tool", resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(abandoned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned temporary file survived: %v", err)
	}
	for _, path := range []string{fresh, foreign} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s was removed: %v", filepath.Base(path), err)
		}
	}
}

func correlationPath(t *testing.T, dir, kind, identifier string) string {
	t.Helper()
	name, err := correlationName(kind, "claude", "session", identifier)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

func TestCorrelationStoreReclaimsShortPendingLeaseButKeepsActiveAgent(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	if err := store.reserve(t.Context(), "claude", "session", "stale-tool", resolution); err != nil {
		t.Fatal(err)
	}
	pending, err := correlationName("pending", "claude", "session", "stale-tool")
	if err != nil {
		t.Fatal(err)
	}
	oldPending := time.Now().Add(-6 * time.Minute)
	if err := os.Chtimes(filepath.Join(dir, pending), oldPending, oldPending); err != nil {
		t.Fatal(err)
	}
	if err := store.reserve(t.Context(), "claude", "session", "stale-tool", resolution); err != nil {
		t.Fatalf("five-minute pending lease was not reclaimed: %v", err)
	}
	if err := store.promote(t.Context(), "claude", "session", "stale-tool", "active-agent"); err != nil {
		t.Fatal(err)
	}
	active, err := correlationName("active", "claude", "session", "active-agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, active), oldPending, oldPending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.active(t.Context(), "claude", "session", "active-agent"); err != nil {
		t.Fatalf("active agent was reaped on the pending lease: %v", err)
	}
}

func TestCorrelationStoreConcurrentReservations(t *testing.T) {
	store, err := newCorrelationStore(t.Context(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	const workers = 16
	errorsSeen := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			errorsSeen <- store.reserve(context.Background(), "claude", "session", fmt.Sprintf("tool-%d", worker), resolution)
		}(index)
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// holdCorrelationLock takes the store lock in dir as a live holder would and releases it after
// hold. The returned channel yields the release's error.
func holdCorrelationLock(t *testing.T, dir string, hold time.Duration) <-chan error {
	t.Helper()
	if err := util.MkdirSecure(dir, util.SecureDirPerm); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, correlationLockName)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	time.AfterFunc(hold, func() { released <- os.Remove(lock) })
	return released
}

// A holder that keeps the lock past the earlier fixed 400 ms is waited for, not reported as a
// store that "remained locked", as long as the lock comes free inside the wait bound (#558).
func TestCorrelationStoreWaitsForASlowHolder(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	const hold = 700 * time.Millisecond
	if store.lockWaitBound() <= hold {
		t.Fatalf("lock wait %s does not outlast the %s hold", store.lockWaitBound(), hold)
	}
	released := holdCorrelationLock(t, dir, hold)
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	began := time.Now()
	if err := store.reserve(t.Context(), "claude", "session", "tool", resolution); err != nil {
		t.Fatalf("reserve behind a %s holder: %v", hold, err)
	}
	if took := time.Since(began); took < hold {
		t.Fatalf("reserve took %s and so did not wait for the %s holder", took, hold)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}

// A lock held past the wait bound, or past the caller's deadline, is an error, and the wait
// ends at whichever comes first.
func TestCorrelationStoreLockWaitEndsAtItsBound(t *testing.T) {
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	cases := []struct {
		name     string
		lockWait time.Duration
		deadline time.Duration
		want     func(error) bool
	}{
		{"wait bound", 150 * time.Millisecond, time.Minute, func(err error) bool {
			return err != nil && strings.Contains(err.Error(), "remained locked for 150ms")
		}},
		{"caller deadline", time.Minute, 150 * time.Millisecond, func(err error) bool {
			return errors.Is(err, context.DeadlineExceeded)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			released := holdCorrelationLock(t, dir, 600*time.Millisecond)
			store := correlationStore{dir: dir, lockWait: tc.lockWait}
			ctx, cancel := context.WithTimeout(t.Context(), tc.deadline)
			defer cancel()
			// The holder releases at 600 ms, so an error proves the wait ended at its bound.
			if err := store.reserve(ctx, "claude", "session", "tool", resolution); !tc.want(err) {
				t.Fatalf("reserve against a held lock: %v", err)
			}
			if err := <-released; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A wait bound shorter than one retry delay still tries the lock once: a free lock is taken,
// a held one is reported without waiting.
func TestCorrelationStoreLockWaitBelowOneDelayTriesOnce(t *testing.T) {
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	free := correlationStore{dir: t.TempDir(), lockWait: time.Nanosecond}
	if err := free.reserve(t.Context(), "claude", "session", "tool", resolution); err != nil {
		t.Fatalf("a free lock was not taken: %v", err)
	}
	dir := t.TempDir()
	released := holdCorrelationLock(t, dir, 300*time.Millisecond)
	held := correlationStore{dir: dir, lockWait: time.Nanosecond}
	err := held.reserve(t.Context(), "claude", "session", "tool", resolution)
	if err == nil || !strings.Contains(err.Error(), "remained locked") {
		t.Fatalf("a held lock was not reported: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if (correlationStore{}).lockWaitBound() != correlationLockWait {
		t.Fatalf("an unset lock wait is %s, want %s", (correlationStore{}).lockWaitBound(), correlationLockWait)
	}
}

func TestCorrelationStoreReclaimsStaleLock(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-correlationLockTTL - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	if err := store.reserve(t.Context(), "claude", "session", "tool", resolution); err != nil {
		t.Fatalf("stale lock was not reclaimed: %v", err)
	}
}

// fillCorrelationDir writes count empty files named prefix-<n> into dir, aged by age.
func fillCorrelationDir(t *testing.T, dir, prefix string, count int, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	for index := 0; index < count; index++ {
		path := filepath.Join(dir, fmt.Sprintf("%s-%04d", prefix, index))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCorrelationStoreScanBoundIsNotTheRowCap: the sweep reads up to correlationScanLimit
// entries, the lock included, and fails closed past it with the bound named. Exactly the
// bound is read in full.
func TestCorrelationStoreScanBoundIsNotTheRowCap(t *testing.T) {
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	atBound := t.TempDir()
	fillCorrelationDir(t, atBound, "foreign", correlationScanLimit-1, 0)
	store, err := newCorrelationStore(t.Context(), "", atBound)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reserve(t.Context(), "claude", "session", "tool", resolution); err != nil {
		t.Fatalf("%d entries with the lock were refused: %v", correlationScanLimit, err)
	}
	pastBound := t.TempDir()
	fillCorrelationDir(t, pastBound, "foreign", correlationScanLimit, 0)
	if store, err = newCorrelationStore(t.Context(), "", pastBound); err != nil {
		t.Fatal(err)
	}
	err = store.reserve(t.Context(), "claude", "session", "tool", resolution)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d entries", correlationScanLimit)) {
		t.Fatalf("%d entries with the lock: %v", correlationScanLimit+1, err)
	}
	requireStoreRows(t, pastBound, 0)
}

// TestCorrelationStoreFullOfRowsSurvivesDebris: a full store plus abandoned temporary files
// and foreign entries, together far past the row cap, still takes a launch. The launch
// evicts one row, the sweep removes the abandoned files, and the foreign ones stay.
func TestCorrelationStoreFullOfRowsSurvivesDebris(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	for index := 0; index < MaxCorrelationEntries; index++ {
		if err := store.reserve(t.Context(), "claude", "session", fmt.Sprintf("tool-%03d", index), resolution); err != nil {
			t.Fatalf("reserve %d: %v", index, err)
		}
	}
	fillCorrelationDir(t, dir, util.AtomicTempPrefix+"pending-abandoned.json", 16, correlationLockTTL+time.Minute)
	fillCorrelationDir(t, dir, "foreign", 2*MaxCorrelationEntries, correlationActiveTTL+time.Hour)
	if err := store.reserve(t.Context(), "claude", "session", "launch", resolution); err != nil {
		t.Fatalf("launch refused by debris: %v", err)
	}
	requireStoreRows(t, dir, MaxCorrelationEntries)
	if matches, err := filepath.Glob(filepath.Join(dir, util.AtomicTempPrefix+"*")); err != nil || len(matches) != 0 {
		t.Fatalf("abandoned temporary files survived the sweep: %v, %v", matches, err)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "foreign-*")); err != nil || len(matches) != 2*MaxCorrelationEntries {
		t.Fatalf("foreign entries = %d (%v), want all %d kept", len(matches), err, 2*MaxCorrelationEntries)
	}
}

// TestLockContended_3D: positive, an existing lock file is contention on every platform and a
// pending-delete refusal is contention on Windows; negative, the same permission error elsewhere
// and any other error are real failures; boundary, no error is not contention.
func TestLockContended_3D(t *testing.T) {
	exist := &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrExist}
	denied := &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrPermission}
	for _, tc := range []struct {
		name string
		err  error
		goos string
		want bool
	}{
		{"existing lock on linux", exist, "linux", true},
		{"existing lock on windows", exist, "windows", true},
		{"pending delete on windows", denied, "windows", true},
		{"permission denied on linux", denied, "linux", false},
		{"other error on windows", &os.PathError{Op: "openat", Path: ".lock", Err: os.ErrInvalid}, "windows", false},
		{"no error", nil, "windows", false},
	} {
		if got := lockContended(tc.err, tc.goos); got != tc.want {
			t.Errorf("%s: lockContended = %v, want %v", tc.name, got, tc.want)
		}
	}
}
