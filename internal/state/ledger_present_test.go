package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Positive: an initialized ledger is present, and so is a working directory holding a
// single ledger file, because a partial ledger is still a ledger the audit must judge.
func TestLedgerPresentFindsInitializedAndPartialLedgers(t *testing.T) {
	full := t.TempDir()
	if err := InitWorkingDir(full); err != nil {
		t.Fatal(err)
	}
	partial := t.TempDir()
	mkdirLedgerFixture(t, filepath.Join(partial, WorkingDirName))
	writeIntegrityFile(t, filepath.Join(partial, WorkingDirName, "BUGS.md"), defaultBugsMD())
	for name, dir := range map[string]string{"initialized": full, "partial": partial} {
		if present, err := LedgerPresent(t.Context(), dir); err != nil || !present {
			t.Fatalf("%s ledger not found: %v, %v", name, present, err)
		}
	}
}

// Negative: a missing context, a canceled context and a project root that does not exist
// are errors, not an absent ledger a caller would go on to seed.
func TestLedgerPresentRejectsInvalidProbes(t *testing.T) {
	dir := t.TempDir()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	cases := map[string]struct {
		ctx  context.Context
		root string
	}{
		"nil context":      {nil, dir},
		"canceled context": {canceled, dir},
		"missing root":     {t.Context(), filepath.Join(dir, "absent")},
	}
	for name, tc := range cases {
		if present, err := LedgerPresent(tc.ctx, tc.root); err == nil || present {
			t.Fatalf("%s accepted: %v, %v", name, present, err)
		}
	}
}

// Boundary: no working directory, a working directory holding only other private files,
// and a regular file under the working directory's name all hold no ledger.
func TestLedgerPresentBoundaries(t *testing.T) {
	absent := t.TempDir()
	ledgerless := t.TempDir()
	mkdirLedgerFixture(t, filepath.Join(ledgerless, WorkingDirName, "evidence"))
	writeIntegrityFile(t, filepath.Join(ledgerless, WorkingDirName, "evidence", "log.txt"), "private\n")
	file := t.TempDir()
	writeIntegrityFile(t, filepath.Join(file, WorkingDirName), "not a directory\n")
	for name, dir := range map[string]string{"absent": absent, "ledgerless": ledgerless, "file": file} {
		if present, err := LedgerPresent(t.Context(), dir); err != nil || present {
			t.Fatalf("%s working directory reported a ledger: %v, %v", name, present, err)
		}
	}
}

// Boundary: a symlink under the working directory's name is never followed, so a ledger
// behind it is not reported as this project's.
func TestLedgerPresentNeverFollowsAWorkingDirectoryLink(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := InitWorkingDir(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, WorkingDirName), filepath.Join(dir, WorkingDirName)); err != nil {
		t.Skipf("symlinks unavailable on this host (Windows without developer mode): %v", err)
	}
	if present, err := LedgerPresent(t.Context(), dir); err != nil || present {
		t.Fatalf("linked ledger reported present: %v, %v", present, err)
	}
}

// mkdirLedgerFixture creates a private fixture directory and its parents.
func mkdirLedgerFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
