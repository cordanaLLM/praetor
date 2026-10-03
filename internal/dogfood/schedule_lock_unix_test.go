//go:build unix

package dogfood

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The schedule lock goes through util.LockPrivateFile, whose tests cover concurrent process
// starts. Here: an absent lock is reported as absent when the tick may not create it, a
// second tick sees the lock busy, a descriptor copy such as a forked child holds does not
// keep the lock past its release (it does against a close-only release), and a second
// release is reported.
func TestLockScheduleReleaseDropsLockHeldByForkedCopy(t *testing.T) {
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
	if _, _, err := lockSchedule(root, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent schedule lock: %v", err)
	}
	release, busy, err := lockSchedule(root, true)
	if err != nil || busy {
		t.Fatalf("first tick: busy=%v err=%v", busy, err)
	}
	if second, busy, err := lockSchedule(root, false); err != nil || !busy || second != nil {
		t.Fatalf("second tick: busy=%v err=%v", busy, err)
	}
	testsupport.CopyDescriptorsOf(t, filepath.Join(dir, "schedule.lock"))
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, busy, err := lockSchedule(root, false)
	if err != nil || busy {
		t.Fatalf("schedule lock still held by a descriptor copy after release: busy=%v err=%v", busy, err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second release not reported: %v", err)
	}
}
