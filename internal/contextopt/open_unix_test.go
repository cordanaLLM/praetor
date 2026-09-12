//go:build unix

// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNonblockingOpenRejectsFileReplacedByFIFO(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"policy.md": []byte("before")})
	path := filepath.Join(root, "policy.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pinned.Close(); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	go func() {
		file, err := openSource(pinned, "policy.md")
		if err != nil {
			done <- err
			return
		}
		_, readErr := stableRead(context.Background(), file, before)
		if err := file.Close(); err != nil {
			done <- err
			return
		}
		done <- readErr
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO replacement was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("source open blocked on FIFO")
	}
}
