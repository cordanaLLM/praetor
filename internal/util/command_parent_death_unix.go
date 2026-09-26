//go:build unix && !linux

package util

import "syscall"

// parentDeathSignal is 0 here: macOS and the other Unix systems praetor builds for have no
// parent-death signal the command could inherit (command_parent_death_linux.go). A supervisor
// that ends praetor with SIGKILL leaves its commands running, so the supervisors in this
// repository send a catchable signal first and wait before they kill
// (.config/lefthook/scripts/common.py stop_process_group).
const parentDeathSignal syscall.Signal = 0

// commandSysProcAttr starts a command as the leader of a process group of its own.
func commandSysProcAttr(syscall.Signal) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
