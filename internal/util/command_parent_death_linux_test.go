//go:build linux

// The parent-death signal is a Linux facility (PR_SET_PDEATHSIG). macOS has none, so there a
// supervisor must send a catchable signal before it kills (command_parent_death_unix.go).

package util

import (
	"syscall"
	"testing"
)

// parentDeathEnv gives the helper's command the parent-death signal praetor gives it.
const parentDeathEnv = "PRAETOR_COMMAND_PARENT_DEATH=1"

// A supervisor such as .config/lefthook/scripts/common.py used to end praetor with SIGKILL on
// praetor's process group. Nothing can catch or forward that signal, and the command's group
// of its own is not in praetor's, so the command used to run on after praetor died.
func TestRunCommand_Positive_KilledParentGroupEndsCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript, parentDeathEnv)
	signalHelper(t, helper, syscall.SIGKILL, true)
	if markerAfterPause(t, dir, ready) {
		t.Fatal("the command outlived a parent killed with its process group")
	}
}

// The fixture must be able to fail: without the parent-death signal the same kill leaves the
// command running, and its marker appears.
func TestRunCommand_Negative_KilledParentWithoutParentDeathSignalOrphansCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript)
	signalHelper(t, helper, syscall.SIGKILL, true)
	if !markerAfterPause(t, dir, ready) {
		t.Fatal("the fixture reported no orphan where one must exist")
	}
}

// Killing praetor alone, not its group, ends the command the same way; 0 gives the command no
// parent-death signal, and every command runs as the leader of its own group either way.
func TestCommandSysProcAttr_Boundary_ParentDeathSignal(t *testing.T) {
	t.Parallel()
	if got := newCommandGroupRegistry().parentDeath; got != syscall.SIGKILL {
		t.Fatalf("commands get parent-death signal %v, want SIGKILL", got)
	}
	for _, sig := range []syscall.Signal{0, syscall.SIGKILL} {
		attr := commandSysProcAttr(sig)
		if !attr.Setpgid || attr.Pdeathsig != sig {
			t.Fatalf("commandSysProcAttr(%v) = %+v", sig, attr)
		}
	}
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript, parentDeathEnv)
	signalHelper(t, helper, syscall.SIGKILL, false)
	if markerAfterPause(t, dir, ready) {
		t.Fatal("the command outlived a parent killed on its own")
	}
}
