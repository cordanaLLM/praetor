//go:build unix

// Process groups and POSIX signals exist only on Unix. command_bytes_other.go keeps
// exec.CommandContext's direct-child handling elsewhere and says what differs.

package util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// commandBytesCleanup keeps descendants in a process group the command owns and records that
// group in runningCommandGroups, so a signal that ends this process reaches the command too
// (TerminateCommandsOnSignal).
func commandBytesCleanup(cmd *exec.Cmd) (start func() error, cleanup func() error) {
	return runningCommandGroups.track(cmd)
}

// signalProcessGroup sends sig to the process group led by pid. A group that no longer exists
// is reported as os.ErrProcessDone.
func signalProcessGroup(pid int, sig syscall.Signal) error {
	err := syscall.Kill(-pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// killProcessGroup kills the process group led by pid. A group that no longer exists is
// reported as os.ErrProcessDone.
func killProcessGroup(pid int) error {
	return signalProcessGroup(pid, syscall.SIGKILL)
}

// errCommandsTerminated refuses a command started after the running ones were stopped
// because this process is ending on a signal.
var errCommandsTerminated = errors.New("util: this process is ending on a signal; command not started")

// commandGroupRegistry records the process group of every running bounded command.
//
// A command's own process group keeps a terminal's Ctrl-C from reaching it, and nothing in
// the command tree observes that signal, so without this record a signalled praetor would
// die and leave its commands running.
type commandGroupRegistry struct {
	mu sync.Mutex
	// groups maps each group leader's pid to a channel closed once its command returned and
	// the rest of its group was killed.
	groups     map[int]<-chan struct{}
	terminated bool
}

// runningCommandGroups is the process-wide registry commandBytesCleanup records into.
var runningCommandGroups = newCommandGroupRegistry()

func newCommandGroupRegistry() *commandGroupRegistry {
	return &commandGroupRegistry{groups: make(map[int]<-chan struct{})}
}

// track makes cmd run in a process group of its own, recorded in r while it runs.
//
// Cancellation asks the group to stop with SIGTERM, which git and most tools answer by
// removing their lock and temporary files; exec.Cmd kills the direct child CommandWaitDelay
// later if it is still running. start starts the command and records the group. cleanup,
// called once the command returned, kills what is left of the group and forgets it, so a
// grandchild cannot outlive the call.
func (r *commandGroupRegistry) track(cmd *exec.Cmd) (start func() error, cleanup func() error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return signalProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
	returned := make(chan struct{})
	start = func() error { return r.start(cmd, returned) }
	cleanup = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := killProcessGroup(cmd.Process.Pid)
		// Closed only after the kill: terminate treats a returned command's group as done.
		close(returned)
		r.release(cmd.Process.Pid)
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return start, cleanup
}

// start starts cmd and records its group with returned, the channel closed once the command
// returned. It holds the registry across Start, so a concurrent terminate either reaches the
// new group or refuses the start; it never misses it.
func (r *commandGroupRegistry) start(cmd *exec.Cmd, returned <-chan struct{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminated {
		return errCommandsTerminated
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.groups[cmd.Process.Pid] = returned
	return nil
}

// release forgets the group led by pid once its command returned.
func (r *commandGroupRegistry) release(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.groups, pid)
}

// terminate stops every recorded group the way sig asks and refuses every later start.
//
// It forwards sig to each group, so each command receives the signal it would have received
// in praetor's own process group and runs its own cleanup: git removes its index and ref
// locks and the clone or worktree it was creating. It then waits until each command has
// returned or graceOver is closed, and kills the groups whose command has not returned.
//
// It returns with the registry still locked; unlock releases it. The signal path never
// unlocks: a goroutine whose command just ended then waits in release instead of reporting
// the failure, so the signal re-raised next, not a failure message, decides how the process
// ends.
func (r *commandGroupRegistry) terminate(sig syscall.Signal, graceOver <-chan struct{}) (unlock func(), err error) {
	r.mu.Lock()
	r.terminated = true
	for pid := range r.groups {
		err = errors.Join(err, signalGroupReporting(pid, sig))
	}
	for pid, returned := range r.groups {
		if !commandReturned(returned, graceOver) {
			err = errors.Join(err, signalGroupReporting(pid, syscall.SIGKILL))
		}
	}
	return r.mu.Unlock, err
}

// signalGroupReporting sends sig to the group led by pid and describes a failure to send it.
// A group that is already gone is not a failure.
func signalGroupReporting(pid int, sig syscall.Signal) error {
	if err := signalProcessGroup(pid, sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("send %v to command process group %d: %w", sig, pid, err)
	}
	return nil
}

// commandReturned waits until returned or graceOver is closed and reports whether the
// command returned. A command that has already returned counts as returned even when the
// grace is over too, so a grace that has exactly elapsed never kills a finished command.
func commandReturned(returned, graceOver <-chan struct{}) bool {
	select {
	case <-returned:
		return true
	default:
	}
	select {
	case <-returned:
		return true
	case <-graceOver:
		return false
	}
}
