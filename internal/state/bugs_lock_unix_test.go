//go:build unix

package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The bug ledger lock goes through util.LockPrivateFile, whose tests cover concurrent process
// starts. Here: a second writer is refused with this lock's message, a descriptor copy such as
// a forked child holds does not keep the lock past its release (it does against a close-only
// release), and a second release is reported.
func TestLockBugLedgerReleaseDropsLockHeldByForkedCopy(t *testing.T) {
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
	release, err := lockBugLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockBugLedger(root); err == nil || second != nil || !strings.Contains(err.Error(), "bug ledger is busy") {
		t.Fatalf("second writer: %v", err)
	}
	testsupport.CopyDescriptorsOf(t, filepath.Join(dir, bugLockName))
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := lockBugLedger(root)
	if err != nil {
		t.Fatalf("bug ledger lock still held by a descriptor copy after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release not reported: %v", err)
	}
}
