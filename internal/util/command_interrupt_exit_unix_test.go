//go:build unix

// endOnSignal's exit hook is Unix-only for the same reason as the rest of signal forwarding;
// on Windows TerminateCommandsOnSignal is a no-op (command_bytes_other.go).

package util

import "testing"

// foreignSignal is an os.Signal that is not a syscall.Signal, the one input that takes
// endOnSignal's exit path without re-raising a real signal at the test process.
type foreignSignal struct{}

func (foreignSignal) String() string { return "foreign" }
func (foreignSignal) Signal()        {}

// TestEndOnSignal_Positive_ExitReceivesTheStatus: the process ends through the exit the entry
// point handed in, never through a library-owned os.Exit.
func TestEndOnSignal_Positive_ExitReceivesTheStatus(t *testing.T) {
	var codes []int
	endOnSignal(newCommandGroupRegistry(), foreignSignal{}, func(code int) { codes = append(codes, code) })
	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit calls = %v, want exactly [1]", codes)
	}
}

// TestEndOnSignal_Negative_NilExitReturns: without an exit there is no last resort, and the
// handler returns instead of ending the process itself.
func TestEndOnSignal_Negative_NilExitReturns(t *testing.T) {
	endOnSignal(newCommandGroupRegistry(), foreignSignal{}, nil)
}

// TestExitWith_Boundary_ZeroStatusStillCalls: a zero status is a real status, not "unset".
func TestExitWith_Boundary_ZeroStatusStillCalls(t *testing.T) {
	called := -1
	exitWith(func(code int) { called = code }, 0)
	if called != 0 {
		t.Fatalf("exit got %d, want 0", called)
	}
	exitWith(nil, 0)
}
