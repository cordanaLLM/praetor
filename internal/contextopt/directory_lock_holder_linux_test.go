// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build linux

package contextopt

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// devOf builds the Linux device number glibc's makedev(3) returns for major and minor.
func devOf(major, minor uint64) uint64 {
	return (major&0xfff)<<8 | (major&^0xfff)<<32 | minor&0xff | (minor&^0xff)<<12
}

func TestFlockHolders(t *testing.T) {
	dev := devOf(0x103, 0x2a1) // three hex digits each side of the split
	if linuxMajor(dev) != 0x103 || linuxMinor(dev) != 0x2a1 {
		t.Fatalf("device split: %x:%x", linuxMajor(dev), linuxMinor(dev))
	}
	locks := strings.Join([]string{
		"1: POSIX  ADVISORY  WRITE 11 103:2a1:4242 0 EOF",    // a record lock, not this lock
		"2: FLOCK  ADVISORY  WRITE 22 103:2a1:4242 0 EOF",    // the holder
		"2: -> FLOCK  ADVISORY  WRITE 33 103:2a1:4242 0 EOF", // a waiter holds nothing
		"3: FLOCK  ADVISORY  WRITE 44 103:2a1:14242 0 EOF",   // another inode ending in the same digits
		"4: FLOCK  ADVISORY  WRITE 55 00:1e:4242 0 EOF",      // the same inode number on another device
		"5: FLOCK  ADVISORY  WRITE -1 103:2a1:4242 0 EOF",    // a holder outside this pid namespace
		"garbage",
		"",
	}, "\n")
	if got := flockHolders([]byte(locks), dev, 4242); !slices.Equal(got, []int{22}) {
		t.Fatalf("exact device and inode: %v", got)
	}
	// Boundary: no line matches the device stat reports, as on a btrfs subvolume; the lock on
	// the same inode number is the holder.
	if got := flockHolders([]byte(locks), devOf(0, 0x3e), 4242); !slices.Equal(got, []int{22, 55}) {
		t.Fatalf("inode fallback: %v", got)
	}
	if got := flockHolders([]byte(locks), dev, 99); len(got) != 0 {
		t.Fatalf("unlocked inode: %v", got)
	}
	if got := flockHolders(nil, dev, 4242); len(got) != 0 {
		t.Fatalf("empty table: %v", got)
	}
}

// The holder of a lock this process takes on a real directory is found in the real
// /proc/locks and named as this process.
func TestDirectoryLockHolderReadsProcLocks(t *testing.T) {
	if _, err := os.Stat(procLocksPath); err != nil {
		t.Skipf("%s unavailable: %v", procLocksPath, err)
	}
	root, _ := pinnedDirectory(t)
	if got := directoryLockHolder(root); !strings.Contains(got, "lists no holder") {
		t.Fatalf("free directory: %s", got)
	}
	holdPlatformLock(t, root)
	want := fmt.Sprintf("process %d (this process)", os.Getpid())
	if got := directoryLockHolder(root); got != want {
		t.Fatalf("held directory: %q, want %q", got, want)
	}
}
