package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestCorrelationStoreRejectsUnboundedDirectory(t *testing.T) {
	dir := t.TempDir()
	store, err := newCorrelationStore(t.Context(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxCorrelationEntries+correlationDirOverhead+1; index++ {
		path := filepath.Join(dir, fmt.Sprintf("junk-%03d", index))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolution := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent"}
	if err := store.reserve(t.Context(), "claude", "session", "tool", resolution); err == nil {
		t.Fatal("unbounded correlation directory was accepted")
	}
}
