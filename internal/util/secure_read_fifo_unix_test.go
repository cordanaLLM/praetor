//go:build unix

package util

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe at a configuration path is refused before any open. Opening it would block
// until a writer appears, and no deadline interrupts a blocked open(2). The read runs in a
// goroutine so a regression fails this test at the limit instead of hanging the binary.
// Unix only: mkfifo does not exist on Windows, where no FIFO can sit at a filesystem path.
func TestReadConfinedLimited_Negative_FIFORefusedWithoutOpening(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "cfg.yml"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadConfinedLimited(root, "cfg.yml", 1<<10)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegularFile) {
			t.Fatalf("FIFO read error = %v, want ErrNotRegularFile", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReadConfinedLimited blocked opening a FIFO")
	}
}

// The pinned reader refuses a named pipe before any open as well.
func TestReadLimitedIn_Negative_FIFORefusedWithoutOpening(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "0001-pipe.md"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	root := openTestRoot(t, dir)
	done := make(chan error, 1)
	go func() {
		_, err := ReadLimitedIn(root, "0001-pipe.md", 1<<10)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegularFile) {
			t.Fatalf("FIFO read error = %v, want ErrNotRegularFile", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReadLimitedIn blocked opening a FIFO")
	}
}
