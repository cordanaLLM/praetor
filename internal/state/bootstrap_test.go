package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBootstrapCreatesPrivateStateOnce(t *testing.T) {
	dir := t.TempDir()
	outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || outcome != BootstrapCreated {
		t.Fatalf("bootstrap failed: outcome=%v, %v", outcome, err)
	}
	report, err := AuditWorkingDir(dir)
	if err != nil || !report.Valid {
		t.Fatalf("new ledger is invalid: %+v, %v", report, err)
	}
	path := filepath.Join(dir, WorkingDirName, "STATE.md")
	writeIntegrityFile(t, path, "preserve private history")
	outcome, err = InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || outcome != BootstrapKept {
		t.Fatalf("existing ledger reinitialized: %v, %v", outcome, err)
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
			if outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil || outcome != BootstrapKept {
				t.Fatalf("existing ledger initialization attempted: %v, %v", outcome, err)
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
	if outcome, err := InitWorkingDirIfAbsentContext(ctx, dir); outcome != BootstrapUnknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bootstrap changed state: %v, %v", outcome, err)
	}
	var absent context.Context
	if outcome, err := InitWorkingDirIfAbsentContext(absent, dir); outcome != BootstrapUnknown || err == nil {
		t.Fatalf("nil context accepted: %v, %v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(dir, WorkingDirName)); !os.IsNotExist(err) {
		t.Fatalf("failed bootstrap created state: %v", err)
	}
	if outcome, err := InitWorkingDirIfAbsentContext(t.Context(), filepath.Join(dir, "missing")); outcome != BootstrapUnknown || err == nil {
		t.Fatalf("missing project accepted: %v, %v", outcome, err)
	}
}

func TestBootstrapNeverFollowsExistingWorkingDirectoryLink(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, WorkingDirName)); err != nil {
		t.Fatal(err)
	}
	if outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir); outcome != BootstrapUnseedable || err != nil {
		t.Fatalf("existing linked path should be left for strict audit: %v, %v", outcome, err)
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
	outcomes := make([]BootstrapOutcome, 8)
	errorsFound := make([]error, len(outcomes))
	for i := range outcomes {
		wg.Go(func() { outcomes[i], errorsFound[i] = InitWorkingDirIfAbsentContext(t.Context(), dir) })
	}
	wg.Wait()
	count := 0
	for i, result := range outcomes {
		if errorsFound[i] != nil {
			t.Fatal(errorsFound[i])
		}
		if result == BootstrapCreated {
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

// TestConcurrentBootstrapNeverExposesAPartialLedgerFile pins the window the previous
// commit opened. Seeding is no longer arbitrated by the single Mkdir winner: every
// process that observes a ledgerless directory writes the five files. With a plain
// exclusive create, a loser that stats a ledger name between the winner's OpenFile and
// its first write sees a zero-byte regular file, reports the ledger present, and the
// audit CLAUDE.md requires next fails on an empty BUGS.md. Ledger files are now staged
// and linked into place, so a name that exists holds its whole template.
// TestConcurrentBootstrapHasOnlyOneCreator cannot catch this: it audits after Wait.
func TestConcurrentBootstrapNeverExposesAPartialLedgerFile(t *testing.T) {
	// maxRaceRounds bounds the search (HISS-02). Against a plain exclusive create the
	// window is narrow - one round in roughly twelve caught it when this was measured -
	// so the test repeats until it is found or the bound is reached.
	const maxRaceRounds = 256
	for round := 0; round < maxRaceRounds; round++ {
		if sighting := watchOneConcurrentBootstrap(t); sighting != "" {
			t.Fatalf("round %d: a concurrent reader saw a partial ledger file: %s", round, sighting)
		}
	}
}

// watchOneConcurrentBootstrap bootstraps one fresh directory from several goroutines while
// another goroutine watches the ledger names, and reports the first name it caught holding
// anything other than its whole template.
func watchOneConcurrentBootstrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	working := filepath.Join(dir, WorkingDirName)
	sizes := make(map[string]int64, len(ledgerTemplates()))
	for _, file := range ledgerTemplates() {
		sizes[file.name] = int64(len(file.content))
	}

	// maxWatchRounds bounds the observer loop (HISS-02); it stops earlier on the signal.
	const maxWatchRounds = 1 << 14
	stop := make(chan struct{})
	short := ""
	var watcher sync.WaitGroup
	watcher.Go(func() { short = watchLedgerSizes(working, sizes, stop, maxWatchRounds) })

	var wg sync.WaitGroup
	failures := make([]error, 8)
	for i := range failures {
		wg.Go(func() { _, failures[i] = InitWorkingDirIfAbsentContext(t.Context(), dir) })
	}
	wg.Wait()
	close(stop)
	watcher.Wait()

	for _, err := range failures {
		if err != nil {
			t.Fatalf("concurrent bootstrap failed: %v", err)
		}
	}
	if report, err := AuditWorkingDir(dir); err != nil || !report.Valid {
		t.Fatalf("concurrent bootstrap left an invalid ledger: %+v, %v", report, err)
	}
	return short
}

// watchLedgerSizes polls the ledger names until stop closes or the round bound is spent.
func watchLedgerSizes(working string, sizes map[string]int64, stop <-chan struct{}, rounds int) string {
	for round := 0; round < rounds; round++ {
		select {
		case <-stop:
			return ""
		default:
		}
		for name, want := range sizes {
			info, err := os.Stat(filepath.Join(working, name))
			if err == nil && info.Size() != want {
				return fmt.Sprintf("%s held %d of %d bytes", name, info.Size(), want)
			}
		}
	}
	return ""
}

func TestBootstrapSeedsADirectoryAnotherWriterCreated(t *testing.T) {
	dir := t.TempDir()
	// milestone, forge, docdistill, dedupe and hindsight all create .workingdir
	// before any state command runs; the ledger must still be initialized.
	if err := os.Mkdir(filepath.Join(dir, WorkingDirName), 0700); err != nil {
		t.Fatal(err)
	}
	outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir)
	if err != nil || outcome != BootstrapSeeded {
		t.Fatalf("bootstrap over an existing directory: outcome=%v, %v", outcome, err)
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

// TestBootstrapLeavesANestedRepositoryUntouched covers a ledgerless .workingdir that
// is another repository's working tree: a clone (".git" directory) or a linked
// worktree or submodule (".git" file). Seeding it dirtied that repository, which
// made Lefthook fail to stash a staged private gitlink before the commit hook's
// privacy check could report it. Nothing is written, and SyncState still fails closed
// on the missing ledger instead of treating the directory as initialized.
func TestBootstrapLeavesANestedRepositoryUntouched(t *testing.T) {
	for name, makeGit := range map[string]func(string) error{
		"clone":    func(path string) error { return os.Mkdir(path, 0o700) },
		"gitfile":  func(path string) error { return os.WriteFile(path, []byte("gitdir: ../.git/modules/x\n"), 0o600) },
		"with-own": func(path string) error { return os.Mkdir(path, 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			working := filepath.Join(dir, WorkingDirName)
			if err := os.Mkdir(working, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := makeGit(filepath.Join(working, ".git")); err != nil {
				t.Fatal(err)
			}
			want := BootstrapNestedRepository
			if name == "with-own" {
				// Boundary: a repository that already holds a ledger file is kept, as any
				// other directory holding one is.
				writeIntegrityFile(t, filepath.Join(working, "STATE.md"), "own private history")
				want = BootstrapKept
			}
			outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir)
			if err != nil || outcome != want {
				t.Fatalf("outcome=%v, %v; want %v", outcome, err, want)
			}
			for _, ledger := range ledgerFileNames() {
				_, statErr := os.Stat(filepath.Join(working, ledger))
				if name != "with-own" && !os.IsNotExist(statErr) {
					t.Fatalf("bootstrap wrote %s into a nested repository: %v", ledger, statErr)
				}
			}
			if _, err := SyncState(t.Context(), dir, "nested"); err == nil {
				t.Fatal("state sync accepted a nested repository without a complete ledger")
			}
		})
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
	if outcome, err := InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil || outcome != BootstrapKept {
		t.Fatalf("partial ledger reinitialized: %v, %v", outcome, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("partial ledger was silently repaired: %v", err)
	}
}
