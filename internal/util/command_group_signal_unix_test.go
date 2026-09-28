//go:build unix

// Process groups and kill(2) exist only on Unix (command_bytes_other.go keeps Windows commands
// in the console's group), so there is no group signal to replay elsewhere.

package util

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// scriptedKill replays a kernel's answers to kill(2) in order and records each call. Calls
// past the script answer ESRCH.
type scriptedKill struct {
	answers []error
	calls   []killCall
}

type killCall struct {
	pid int
	sig syscall.Signal
}

func (s *scriptedKill) kill(pid int, sig syscall.Signal) error {
	s.calls = append(s.calls, killCall{pid: pid, sig: sig})
	if len(s.calls) > len(s.answers) {
		return syscall.ESRCH
	}
	return s.answers[len(s.calls)-1]
}

func scriptedGroups(polls int, answers ...error) (groupSignaller, *scriptedKill) {
	script := &scriptedKill{answers: answers}
	return groupSignaller{kill: script.kill, polls: polls, delay: time.Millisecond}, script
}

// Positive: Darwin answers EPERM for a group whose members exited but are not yet reaped. Once
// the group disappears (ESRCH to signal 0) the group counts as done, as a vanished one does
// (#558). The signal and every probe address the group, never the leader alone.
func TestGroupSignallerPositive_UnreapedGroupIsDoneOnceGone(t *testing.T) {
	groups, script := scriptedGroups(5, syscall.EPERM, syscall.EPERM, syscall.EPERM, syscall.ESRCH)
	if err := groups.signal(42, syscall.SIGKILL); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("signal = %v, want os.ErrProcessDone", err)
	}
	want := []killCall{{-42, syscall.SIGKILL}, {-42, 0}, {-42, 0}, {-42, 0}}
	if len(script.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", script.calls, want)
	}
	for index, call := range want {
		if script.calls[index] != call {
			t.Fatalf("call %d = %v, want %v", index, script.calls[index], call)
		}
	}
	if err := processGroups.signal(deadGroupLeader(t), 0); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("a reaped group: %v, want os.ErrProcessDone", err)
	}
}

// Negative: EPERM stands while the group still exists, whether it keeps answering EPERM or a
// probe finds a member it may signal, and any other error is returned without probing.
func TestGroupSignallerNegative_EPERMStandsWhileTheGroupExists(t *testing.T) {
	cases := []struct {
		name    string
		answers []error
		want    error
		calls   int
	}{
		{"still refused", []error{syscall.EPERM, syscall.EPERM, syscall.EPERM, syscall.EPERM}, syscall.EPERM, 4},
		{"live member found", []error{syscall.EPERM, nil}, syscall.EPERM, 2},
		{"other error", []error{syscall.EINVAL}, syscall.EINVAL, 1},
		{"delivered", []error{nil}, nil, 1},
	}
	for _, tc := range cases {
		groups, script := scriptedGroups(3, tc.answers...)
		err := groups.signal(7, syscall.SIGTERM)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) || errors.Is(err, os.ErrProcessDone) {
			t.Errorf("%s: signal = %v, want %v", tc.name, err, tc.want)
		}
		if len(script.calls) != tc.calls {
			t.Errorf("%s: %d kill calls, want %d", tc.name, len(script.calls), tc.calls)
		}
	}
}

// Boundary: a group that disappears on the last allowed probe is done, one that disappears a
// probe later is not, a bound of zero never probes, and ESRCH to the signal itself needs no
// probe.
func TestGroupSignallerBoundary_ProbeBound(t *testing.T) {
	cases := []struct {
		name    string
		polls   int
		answers []error
		done    bool
		calls   int
	}{
		{"gone on the last probe", 3, []error{syscall.EPERM, syscall.EPERM, syscall.EPERM, syscall.ESRCH}, true, 4},
		{"gone one probe late", 3, []error{syscall.EPERM, syscall.EPERM, syscall.EPERM, syscall.EPERM, syscall.ESRCH}, false, 4},
		{"no probes", 0, []error{syscall.EPERM, syscall.ESRCH}, false, 1},
		{"gone before the signal", 3, []error{syscall.ESRCH}, true, 1},
	}
	for _, tc := range cases {
		groups, script := scriptedGroups(tc.polls, tc.answers...)
		err := groups.signal(9, syscall.SIGINT)
		if done := errors.Is(err, os.ErrProcessDone); done != tc.done || (!done && !errors.Is(err, syscall.EPERM)) {
			t.Errorf("%s: signal = %v, want done=%t", tc.name, err, tc.done)
		}
		if len(script.calls) != tc.calls {
			t.Errorf("%s: %d kill calls, want %d", tc.name, len(script.calls), tc.calls)
		}
	}
	if processGroups.polls*int(processGroups.delay/time.Millisecond) != 1000 {
		t.Fatalf("the EPERM wait is %d polls of %s, want one second in all", processGroups.polls, processGroups.delay)
	}
}

// deadGroupLeader starts a process in a group of its own, waits for it, and returns its pid:
// a group that no longer exists.
func deadGroupLeader(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
