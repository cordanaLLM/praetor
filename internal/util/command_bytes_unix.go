//go:build unix

package util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// commandBytesCleanup keeps descendants in a process group the command owns. start starts
// the command and records the group in runningCommandGroups, so a signal that ends this
// process can take the group with it (TerminateCommandsOnSignal). Cancellation kills the
// group; cleanup, called once the command returned, kills what is left of it and forgets it.
func commandBytesCleanup(cmd *exec.Cmd) (start func() error, cleanup func() error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return killProcessGroup(cmd.Process.Pid)
	}
	start = func() error { return runningCommandGroups.start(cmd) }
	cleanup = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := cmd.Cancel()
		runningCommandGroups.release(cmd.Process.Pid)
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return start, cleanup
}

// killProcessGroup kills the process group led by pid. A group that no longer exists is
// reported as os.ErrProcessDone.
func killProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// errCommandsTerminated refuses a command started after the running ones were killed
// because this process is ending on a signal.
var errCommandsTerminated = errors.New("util: this process is ending on a signal; command not started")

// commandGroupRegistry records the process group of every running bounded command.
//
// A command's own process group keeps a terminal's Ctrl-C from reaching it, and nothing in
// the command tree observes that signal, so without this record a signalled praetor would
// die and leave its commands running.
type commandGroupRegistry struct {
	mu         sync.Mutex
	groups     map[int]struct{}
	terminated bool
}

// runningCommandGroups is the process-wide registry commandBytesCleanup records into.
var runningCommandGroups = newCommandGroupRegistry()

func newCommandGroupRegistry() *commandGroupRegistry {
	return &commandGroupRegistry{groups: make(map[int]struct{})}
}

// start starts cmd and records its group. It holds the registry across Start, so a
// concurrent terminate either kills the new group or refuses the start; it never misses it.
func (r *commandGroupRegistry) start(cmd *exec.Cmd) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminated {
		return errCommandsTerminated
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.groups[cmd.Process.Pid] = struct{}{}
	return nil
}

// release forgets the group led by pid once its command returned.
func (r *commandGroupRegistry) release(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.groups, pid)
}

// terminate kills every recorded group, refuses every later start, and returns with the
// registry still locked; unlock releases it. The signal path never unlocks: a goroutine
// whose command was just killed then waits in release instead of reporting the kill, so the
// signal re-raised next, not a failure message, decides how the process ends.
func (r *commandGroupRegistry) terminate() (unlock func(), err error) {
	r.mu.Lock()
	r.terminated = true
	for pid := range r.groups {
		if killErr := killProcessGroup(pid); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = errors.Join(err, fmt.Errorf("kill command process group %d: %w", pid, killErr))
		}
	}
	return r.mu.Unlock, err
}
