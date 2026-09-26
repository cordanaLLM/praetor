//go:build unix

// Signal forwarding to command process groups is Unix-only: command_bytes_other.go leaves
// commands in the console's group, where the console's Ctrl-C reaches them directly.

package util

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// terminatingSignals are the signals that end a process by default and that a terminal or
// its shell sends to a whole job: Ctrl-C, kill's default, and hangup.
var terminatingSignals = []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// signalExitGrace bounds how long the process waits to die from its re-raised signal before
// it exits with the shell's 128+signal status instead.
const signalExitGrace = 2 * time.Second

var installSignalTermination sync.Once

// TerminateCommandsOnSignal makes SIGINT, SIGTERM and SIGHUP reach the process group of every
// command running through RunCommand, RunGit, RunCommandBytes or RunCommandStream before
// they end this process the way they do by default.
//
// Each such command runs in its own process group, so a terminal's Ctrl-C reaches only this
// process, which dies on it without running its deferred cleanup. With this handler the
// signal is forwarded to every command group, so git still removes its lock files and the
// clone or worktree it was creating, as it did when it shared this process's group. Commands
// get CommandWaitDelay to exit; groups still running then are killed. Call it early in main,
// in a program that does not handle these signals itself. A signal the process already
// ignores stays ignored, as nohup and background jobs expect. Repeated calls are no-ops.
func TerminateCommandsOnSignal() {
	installSignalTermination.Do(func() {
		watched := make([]os.Signal, 0, len(terminatingSignals))
		for _, sig := range terminatingSignals {
			if !signal.Ignored(sig) {
				watched = append(watched, sig)
			}
		}
		if len(watched) == 0 {
			return
		}
		received := make(chan os.Signal, 1)
		signal.Notify(received, watched...)
		go func() { endOnSignal(runningCommandGroups, <-received) }()
	})
}

// endOnSignal forwards sig to every running command group, waits up to CommandWaitDelay for
// the commands to exit, kills the groups still running, then re-raises sig with its default
// action so the exit status still reads as killed by sig. It returns only if the process
// survives that, which it should not.
func endOnSignal(groups *commandGroupRegistry, sig os.Signal) {
	number, ok := sig.(syscall.Signal)
	if !ok {
		// Unreachable on Unix, where signal.Notify delivers syscall.Signal values; kill's
		// default still lets the commands clean up.
		number = syscall.SIGTERM
	}
	grace, cancel := context.WithTimeout(context.Background(), CommandWaitDelay)
	// The registry stays locked: see commandGroupRegistry.terminate.
	_, err := groups.terminate(number, grace.Done())
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "praetor: %v\n", err)
	}
	if !ok {
		os.Exit(1)
	}
	signal.Reset(sig)
	if err := syscall.Kill(os.Getpid(), number); err != nil {
		fmt.Fprintf(os.Stderr, "praetor: re-raise %v: %v\n", sig, err)
	}
	time.Sleep(signalExitGrace)
	os.Exit(128 + int(number))
}
