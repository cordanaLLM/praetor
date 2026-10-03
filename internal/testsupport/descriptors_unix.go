// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package testsupport

import (
	"syscall"
	"testing"
)

// descriptorScanLimit bounds the descriptor numbers CopyDescriptorsOf inspects. A test
// process holds a few dozen; the bound keeps the scan finite whatever RLIMIT_NOFILE says.
const descriptorScanLimit = 4096

// CopyDescriptorsOf duplicates every descriptor this process holds on the file at path and
// closes the copies when the test ends. The copies share their open file descriptions, as
// the descriptors a subprocess holds between its fork and its exec do, so a flock lock taken
// through one of them stays held for as long as a copy is open unless it is unlocked
// explicitly (util.LockExclusive). It returns how many descriptors it copied and fails the
// test when none refers to path, so a lock test proves it reached the lock's descriptor.
func CopyDescriptorsOf(t testing.TB, path string) int {
	t.Helper()
	var want syscall.Stat_t
	if err := syscall.Stat(path, &want); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	// Collect first: a copy takes the lowest free number, which the scan may not have
	// reached yet, and must not be copied again.
	var matches []int
	for fd := range descriptorScanLimit {
		var got syscall.Stat_t
		if syscall.Fstat(fd, &got) == nil && got.Dev == want.Dev && got.Ino == want.Ino {
			matches = append(matches, fd)
		}
	}
	if len(matches) == 0 {
		t.Fatalf("no open descriptor refers to %s", path)
	}
	for _, fd := range matches {
		copied := dupCloseOnExec(t, fd)
		t.Cleanup(func() {
			if err := syscall.Close(copied); err != nil {
				t.Errorf("close descriptor copy %d of %s: %v", copied, path, err)
			}
		})
	}
	return len(matches)
}

// dupCloseOnExec duplicates fd and marks the copy close-on-exec under the fork lock, so a
// subprocess another test starts never inherits it.
func dupCloseOnExec(t testing.TB, fd int) int {
	t.Helper()
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	copied, err := syscall.Dup(fd)
	if err != nil {
		t.Fatalf("duplicate descriptor %d: %v", fd, err)
	}
	syscall.CloseOnExec(copied)
	return copied
}
