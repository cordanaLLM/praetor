//go:build linux

// The parent-death signal is a Linux facility (PR_SET_PDEATHSIG). macOS has none, so there a
// supervisor must send a catchable signal before it kills (command_parent_death_unix.go).

package util

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// parentDeathEnv gives the helper's command the parent-death signal praetor gives it.
const parentDeathEnv = "PRAETOR_COMMAND_PARENT_DEATH=1"

// A supervisor such as .config/lefthook/scripts/common.py used to end praetor with SIGKILL on
// praetor's process group. Nothing can catch or forward that signal, and the command's group
// of its own is not in praetor's, so the command used to run on after praetor died.
func TestRunCommand_Positive_KilledParentGroupEndsCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript, parentDeathEnv)
	timing := killHelperTimed(t, helper, true, dir, ready)
	if markerAfterPause(t, dir, ready) {
		t.Fatalf("the command outlived a parent killed with its process group: %v", timing)
	}
}

// The fixture must be able to fail: without the parent-death signal the same kill leaves the
// command running, and its marker appears.
func TestRunCommand_Negative_KilledParentWithoutParentDeathSignalOrphansCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript)
	timing := killHelperTimed(t, helper, true, dir, ready)
	if !markerAfterPause(t, dir, ready) {
		t.Fatalf("the fixture reported no orphan where one must exist: %v", timing)
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
	timing := killHelperTimed(t, helper, false, dir, ready)
	if markerAfterPause(t, dir, ready) {
		t.Fatalf("the command outlived a parent killed on its own: %v", timing)
	}
}

// parentDeathTiming is the timeline of one parent-death test. A failure prints it, so a red run
// under load shows when the kill landed and what became of the command, not only the verdict
// (lusoris/praetor#100). It changes no timing and no verdict.
type parentDeathTiming struct {
	dir    string
	ready  time.Time
	sent   time.Time
	reaped time.Duration
}

// killHelperTimed kills the helper with SIGKILL, alone or with its group, and records when.
func killHelperTimed(t *testing.T, helper *exec.Cmd, group bool, dir string, ready time.Time) parentDeathTiming {
	t.Helper()
	sent := time.Now()
	reaped := signalHelper(t, helper, syscall.SIGKILL, group)
	return parentDeathTiming{dir: dir, ready: ready, sent: sent, reaped: reaped}
}

// String reports each step relative to the moment the test saw the command's ready file.
func (p parentDeathTiming) String() string {
	return fmt.Sprintf("ready->kill %v, kill->reap %v (helper died from %v), check at ready+%v; "+
		"command %s; ready file->marker %s; pause %v",
		p.sent.Sub(p.ready), p.reaped, syscall.SIGKILL, time.Since(p.ready),
		p.commandState(), p.markerAge(), interruptPause)
}

// commandState names the command's pid from its ready file and its state in /proc.
func (p parentDeathTiming) commandState() string {
	raw, err := os.ReadFile(filepath.Join(p.dir, "ready"))
	if err != nil {
		return fmt.Sprintf("pid unknown (%v)", err)
	}
	pid := strings.TrimSpace(string(raw))
	stat, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		return "pid " + pid + " gone"
	}
	// The fields after the parenthesised command name are state, then parent pid.
	text := string(stat)
	fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
	if len(fields) < 2 {
		return "pid " + pid + " stat unreadable"
	}
	return "pid " + pid + " state " + fields[0] + " ppid " + fields[1]
}

// markerAge is how long after the command wrote its ready file it wrote the marker.
func (p parentDeathTiming) markerAge() string {
	readyInfo, err := os.Stat(filepath.Join(p.dir, "ready"))
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	markerInfo, err := os.Stat(filepath.Join(p.dir, "marker"))
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	return markerInfo.ModTime().Sub(readyInfo.ModTime()).String()
}
