// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build linux

package contextopt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	// procLocksPath lists every file lock the kernel holds, with the process that took it
	// (proc_locks(5)).
	procLocksPath = "/proc/locks"
	// maxProcLocksBytes and maxProcLockLines bound the read and the parse of procLocksPath.
	// A holder past them goes unnamed; the error still says the directory is busy.
	maxProcLocksBytes = 4 << 20
	maxProcLockLines  = 1 << 16
)

// readProcLocks reads at most maxProcLocksBytes of procLocksPath.
func readProcLocks() ([]byte, error) {
	file, err := os.Open(procLocksPath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxProcLocksBytes))
	return data, errors.Join(readErr, file.Close())
}

// flockHolders returns the processes data, the text of /proc/locks, lists as holding a flock
// lock on inode ino of device dev.
//
// A line reads "1: FLOCK  ADVISORY  WRITE 4242 00:1e:18071854 0 EOF": id, type, mode,
// access, pid, then major:minor:inode with the device numbers in hex. A waiter blocked on a
// lock is listed as "1: -> FLOCK ..." and holds nothing. The kernel lists the device of the
// filesystem, which differs from the one stat reports on a btrfs subvolume, so a lock on the
// same inode number elsewhere is accepted when none matches both.
func flockHolders(data []byte, dev, ino uint64) []int {
	exact := fmt.Sprintf("%02x:%02x:%d", linuxMajor(dev), linuxMinor(dev), ino)
	inode := ":" + strconv.FormatUint(ino, 10)
	var onDevice, onInode []int
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines) && i < maxProcLockLines; i++ {
		pid, id, ok := parseFlockLine(lines[i])
		switch {
		case !ok:
		case id == exact:
			onDevice = append(onDevice, pid)
		case strings.HasSuffix(id, inode) && strings.Count(id, ":") == 2:
			onInode = append(onInode, pid)
		}
	}
	if len(onDevice) > 0 {
		return onDevice
	}
	return onInode
}

// parseFlockLine returns the pid and the device:inode field of a /proc/locks line that lists a
// held flock lock; ok is false for any other line.
func parseFlockLine(line string) (pid int, id string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 || fields[1] != "FLOCK" {
		return 0, "", false
	}
	pid, err := strconv.Atoi(fields[4])
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, fields[5], true
}

// linuxMajor and linuxMinor split a Linux device number as glibc's major(3) and minor(3) do.
func linuxMajor(dev uint64) uint64 { return (dev>>8)&0xfff | (dev>>32)&^uint64(0xfff) }

func linuxMinor(dev uint64) uint64 { return dev&0xff | (dev>>12)&^uint64(0xff) }
