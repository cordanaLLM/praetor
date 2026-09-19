package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBootstrapCreatesPrivateStateOnce(t *testing.T) {
	dir := t.TempDir()
	created, err := InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || !created {
		t.Fatalf("bootstrap failed: created=%v, %v", created, err)
	}
	report, err := AuditWorkingDir(dir)
	if err != nil || !report.Valid {
		t.Fatalf("new ledger is invalid: %+v, %v", report, err)
	}
	path := filepath.Join(dir, WorkingDirName, "STATE.md")
	writeIntegrityFile(t, path, "preserve private history")
	created, err = InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || created {
		t.Fatalf("existing ledger reinitialized: %v, %v", created, err)
	}
	assertIntegrityFile(t, path, "preserve private history")
}

func TestBootstrapPreservesInvalidExistingState(t *testing.T) {
	for _, kind := range []string{"partial", "malformed", "p0"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := InitWorkingDir(dir); err != nil {
				t.Fatal(err)
			}
			bugs := filepath.Join(dir, WorkingDirName, "BUGS.md")
			switch kind {
			case "partial":
				if err := os.Remove(filepath.Join(dir, WorkingDirName, "OPEN.md")); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				writeIntegrityFile(t, bugs, "| `BUG-001` | malformed | p0 | open |\n")
			case "p0":
				if _, err := AddBug(dir, BugEntry{Title: "Existing blocker", Severity: "p0"}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(bugs)
			if err != nil {
				t.Fatal(err)
			}
			if created, err := InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil || created {
				t.Fatalf("existing ledger initialization attempted: %v, %v", created, err)
			}
			assertIntegrityFile(t, bugs, string(before))
			report, err := AuditWorkingDir(dir)
			if err == nil && report.Valid {
				t.Fatalf("bootstrap hid %s failure: %+v", kind, report)
			}
		})
	}
}

func TestBootstrapRejectsCanceledAndInvalidRoots(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if created, err := InitWorkingDirIfAbsentContext(ctx, dir); created || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bootstrap changed state: %v, %v", created, err)
	}
	var absent context.Context
	if created, err := InitWorkingDirIfAbsentContext(absent, dir); created || err == nil {
		t.Fatalf("nil context accepted: %v, %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(dir, WorkingDirName)); !os.IsNotExist(err) {
		t.Fatalf("failed bootstrap created state: %v", err)
	}
	if created, err := InitWorkingDirIfAbsentContext(t.Context(), filepath.Join(dir, "missing")); created || err == nil {
		t.Fatalf("missing project accepted: %v, %v", created, err)
	}
}

func TestBootstrapNeverFollowsExistingWorkingDirectoryLink(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, WorkingDirName)); err != nil {
		t.Fatal(err)
	}
	if created, err := InitWorkingDirIfAbsentContext(t.Context(), dir); created || err != nil {
		t.Fatalf("existing linked path should be left for strict audit: %v, %v", created, err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("bootstrap followed linked ledger: %v, %v", entries, err)
	}
	report, err := AuditWorkingDir(dir)
	if err == nil && report.Valid {
		t.Fatal("linked ledger accepted")
	}
}

func TestConcurrentBootstrapHasOnlyOneCreator(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	created := make([]bool, 8)
	errorsFound := make([]error, len(created))
	for i := range created {
		wg.Go(func() { created[i], errorsFound[i] = InitWorkingDirIfAbsentContext(t.Context(), dir) })
	}
	wg.Wait()
	count := 0
	for i, result := range created {
		if errorsFound[i] != nil {
			t.Fatal(errorsFound[i])
		}
		if result {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one initializer, got %d", count)
	}
	if report, err := AuditWorkingDir(dir); err != nil || !report.Valid {
		t.Fatalf("concurrent bootstrap corrupted ledger: %+v, %v", report, err)
	}
}

func TestBootstrapSeedsADirectoryAnotherWriterCreated(t *testing.T) {
	dir := t.TempDir()
	// milestone, forge, docdistill, dedupe and hindsight all create .workingdir
	// before any state command runs; the ledger must still be initialized.
	if err := os.Mkdir(filepath.Join(dir, WorkingDirName), 0700); err != nil {
		t.Fatal(err)
	}
	created, err := InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || created {
		t.Fatalf("bootstrap over an existing directory: created=%v, %v", created, err)
	}
	for _, name := range ledgerFileNames() {
		if _, err := os.Stat(filepath.Join(dir, WorkingDirName, name)); err != nil {
			t.Fatalf("ledger file %s was not seeded: %v", name, err)
		}
	}
	report, err := AuditWorkingDir(dir)
	if err != nil || !report.Valid {
		t.Fatalf("seeded ledger is invalid: %+v, %v", report, err)
	}
	if _, err := SyncState(t.Context(), dir, "seeded"); err != nil {
		t.Fatalf("state sync over a seeded ledger failed: %v", err)
	}
}

func TestBootstrapLeavesAPartialLedgerForTheAudit(t *testing.T) {
	dir := t.TempDir()
	if err := InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, WorkingDirName, "BACKLOG.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if created, err := InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil || created {
		t.Fatalf("partial ledger reinitialized: %v, %v", created, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("partial ledger was silently repaired: %v", err)
	}
}
