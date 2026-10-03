// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func openFixture(t *testing.T, path string) *os.File {
	t.Helper()
	// #nosec G304 -- path is a file inside this test's own temporary directory.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func flockFile(t *testing.T, path string) error {
	t.Helper()
	file := openFixture(t, path)
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	conn, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var lockErr error
	if err := conn.Control(func(fd uintptr) { lockErr = syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB) }); err != nil {
		t.Fatal(err)
	}
	return lockErr
}

// TestCopyDescriptorsOf_Positive_CopiesShareTheOpenDescription: a lock taken through the
// original stays held after the original closes, for as long as the copy lives, and is free
// once the copy is closed at the end of its test.
func TestCopyDescriptorsOf_Positive_CopiesShareTheOpenDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	t.Run("held", func(t *testing.T) {
		file := openFixture(t, path)
		conn, err := file.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var lockErr error
		if err := conn.Control(func(fd uintptr) { lockErr = syscall.Flock(int(fd), syscall.LOCK_EX) }); err != nil || lockErr != nil {
			t.Fatalf("lock: %v %v", err, lockErr)
		}
		if copies := CopyDescriptorsOf(t, path); copies != 1 {
			t.Fatalf("copied %d descriptors, want 1", copies)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := flockFile(t, path); err == nil {
			t.Fatal("lock was free while a copy of its description was open")
		}
	})
	if err := flockFile(t, path); err != nil {
		t.Fatalf("lock still held after the copy was closed: %v", err)
	}
}

// TestCopyDescriptorsOf_Negative_FailsWithoutADescriptor: a file nothing holds open, or a
// missing path, fails the test instead of returning zero copies.
func TestCopyDescriptorsOf_Negative_FailsWithoutADescriptor(t *testing.T) {
	closed := filepath.Join(t.TempDir(), "closed.lock")
	if err := openFixture(t, closed).Close(); err != nil {
		t.Fatal(err)
	}
	if message := runRecorded(t, func(tb testing.TB) { CopyDescriptorsOf(tb, closed) }); !strings.Contains(message, "no open descriptor refers to") {
		t.Errorf("closed file: %q", message)
	}
	missing := filepath.Join(t.TempDir(), "missing.lock")
	if message := runRecorded(t, func(tb testing.TB) { CopyDescriptorsOf(tb, missing) }); !strings.HasPrefix(message, "stat ") {
		t.Errorf("missing file: %q", message)
	}
}

// TestCopyDescriptorsOf_Boundary_CopiesEachDescriptorOnce: two descriptors on one file give
// two copies; the copies, which the scan may meet later, are not copied again.
func TestCopyDescriptorsOf_Boundary_CopiesEachDescriptorOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.lock")
	for range 2 {
		file := openFixture(t, path)
		t.Cleanup(func() {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	if copies := CopyDescriptorsOf(t, path); copies != 2 {
		t.Fatalf("copied %d descriptors, want 2", copies)
	}
}
