//go:build linux

package util

import "syscall"

// parentDeathSignal is what the kernel sends a command when praetor dies without forwarding a
// signal to it. A supervisor may end praetor with SIGKILL on praetor's own process group, which
// praetor cannot catch and which does not reach a command in a group of its own; SIGKILL on the
// command itself restores the bound the shared group gave. It reaches the command, not the
// processes the command started. Linux sends it when the thread that started the command ends,
// and the Go runtime ends a thread only when a goroutine locked to it exits; nothing in praetor
// locks one (go.dev/issue/27505).
const parentDeathSignal = syscall.SIGKILL

// commandSysProcAttr starts a command as the leader of a process group of its own, which the
// kernel sends parentDeath when praetor dies; 0 sends nothing.
func commandSysProcAttr(parentDeath syscall.Signal) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: parentDeath}
}
