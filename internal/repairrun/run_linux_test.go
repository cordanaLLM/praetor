//go:build linux

package repairrun

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The execution lock goes through util.LockPrivateFile, whose tests cover concurrent process
// starts. Here: an existing state without its lock is reported as absent, a second run sees
// the lock busy, a descriptor copy such as a forked child holds does not keep the lock past
// its release (it does against a close-only release), and a second release is reported.
func TestLockStateReleaseDropsLockHeldByForkedCopy(t *testing.T) {
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
	if _, _, err := lockState(root, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent execution lock: %v", err)
	}
	release, busy, err := lockState(root, true)
	if err != nil || busy {
		t.Fatalf("first run: busy=%v err=%v", busy, err)
	}
	if second, busy, err := lockState(root, false); err != nil || !busy || second != nil {
		t.Fatalf("second run: busy=%v err=%v", busy, err)
	}
	testsupport.CopyDescriptorsOf(t, filepath.Join(dir, "execution.lock"))
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, busy, err := lockState(root, false)
	if err != nil || busy {
		t.Fatalf("execution lock still held by a descriptor copy after release: busy=%v err=%v", busy, err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release not reported: %v", err)
	}
}
