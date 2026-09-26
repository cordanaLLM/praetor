package util

// Linux only: the fault is injected with RLIMIT_FSIZE, and the subprocess harness
// (runPermissionTestChild) lives in secure_permissions_linux_test.go. The rename-based
// write under test is platform-neutral and covered everywhere by
// TestWriteFileNoFollow_Negative_HardLinkedVictimUntouched.

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteFileNoFollow_Boundary_FailedWriteKeepsPreviousLedger pins BUG-827: a write
// that dies part-way must leave the previous milestones.json, BACKLOG.md or project.json
// intact. The old in-place writer truncated the file first, so the same failure left a
// torn prefix of the new contents -- the state a crash between truncate and write leaves.
func TestWriteFileNoFollow_Boundary_FailedWriteKeepsPreviousLedger(t *testing.T) {
	const marker = "PRAETOR_TEST_NOFOLLOW_FSIZE"
	if os.Getenv(marker) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		runPermissionTestChild(t, executable, t.Name(), marker+"=1", nil)
		return
	}
	// This test runs alone in a subprocess: the file-size limit never reaches the parent suite.
	dir := t.TempDir()
	ledger := filepath.Join(dir, "milestones.json")
	previous := []byte(`{"milestones":[]}`)
	if err := os.WriteFile(ledger, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	limitFileSize(t, 64)

	if err := WriteFileNoFollow(ledger, bytes.Repeat([]byte("x"), 4096), 0o600); err == nil {
		t.Fatal("expected the write to fail past RLIMIT_FSIZE")
	}
	data, err := os.ReadFile(ledger) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("the previous ledger must survive the failed write: %v", err)
	}
	if !bytes.Equal(data, previous) {
		t.Errorf("ledger = %q, want the untouched previous contents %q", data, previous)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only milestones.json in %s, got %v", dir, entries)
	}
}

// limitFileSize lowers this process's RLIMIT_FSIZE soft limit for the rest of the test.
// The Go runtime ignores SIGXFSZ unless it is notified, so an oversized write fails with
// EFBIG instead of killing the process.
func limitFileSize(t *testing.T, limit uint64) {
	t.Helper()
	var previous syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
		t.Fatal(err)
	}
	lowered := syscall.Rlimit{Cur: min(limit, previous.Max), Max: previous.Max}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lowered); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
			t.Errorf("restore RLIMIT_FSIZE: %v", err)
		}
	})
}
