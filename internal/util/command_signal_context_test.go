package util

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// unnumberedSignal is an os.Signal that is not a syscall.Signal.
type unnumberedSignal struct{}

func (unnumberedSignal) String() string { return "unnumbered" }
func (unnumberedSignal) Signal()        {}

func TestSignalError_ExitCode_3D(t *testing.T) {
	// Positive: the shell's 128+signal status, so an interrupted run reads as one.
	for sig, want := range map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143, syscall.SIGHUP: 129} {
		interrupted := &SignalError{Signal: sig}
		if got := interrupted.ExitCode(); got != want {
			t.Errorf("SignalError{%v}.ExitCode() = %d, want %d", sig, got, want)
		}
		if !strings.Contains(interrupted.Error(), sig.String()) {
			t.Errorf("SignalError{%v}.Error() = %q, want the signal named", sig, interrupted.Error())
		}
	}
	// Negative: a signal without a number is a plain failure, never a success.
	if got := (&SignalError{Signal: unnumberedSignal{}}).ExitCode(); got != 1 {
		t.Errorf("unnumbered signal exit code = %d, want 1", got)
	}
	// Boundary: signal number zero is no signal and cannot yield status 128.
	if got := (&SignalError{Signal: syscall.Signal(0)}).ExitCode(); got != 1 {
		t.Errorf("signal 0 exit code = %d, want 1", got)
	}
}

// Positive: the first signal stops the watch, reaches the running commands before the context
// is cancelled, and becomes the cancellation cause.
func TestCancelOnSignal_Positive_ForwardsThenCancelsWithTheSignal(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	received := make(chan os.Signal, 1)
	received <- syscall.SIGHUP
	stoppedWatching := false
	var forwarded os.Signal
	forward := func(sig os.Signal) error {
		if ctx.Err() != nil {
			t.Error("the context was cancelled before the signal reached the commands")
		}
		forwarded = sig
		return nil
	}
	cancelOnSignal(received, make(chan struct{}), func() { stoppedWatching = true }, forward, cancel)

	if !stoppedWatching {
		t.Error("the first signal must restore the default action, so a second one ends the process")
	}
	if forwarded != syscall.SIGHUP {
		t.Errorf("forwarded %v, want hangup", forwarded)
	}
	var interrupted *SignalError
	if !errors.As(context.Cause(ctx), &interrupted) || interrupted.Signal != syscall.SIGHUP {
		t.Fatalf("cause = %v, want a SignalError for hangup", context.Cause(ctx))
	}
}

// Negative: a failure to forward the signal is reported in the cause without hiding the signal.
func TestCancelOnSignal_Negative_ForwardFailureJoinsTheCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	received := make(chan os.Signal, 1)
	received <- syscall.SIGINT
	forwardFailure := errors.New("send interrupt to command process group 42: operation not permitted")
	cancelOnSignal(received, make(chan struct{}), func() {}, func(os.Signal) error { return forwardFailure }, cancel)

	cause := context.Cause(ctx)
	var interrupted *SignalError
	if !errors.As(cause, &interrupted) || interrupted.Signal != syscall.SIGINT {
		t.Errorf("cause %v lost the signal", cause)
	}
	if !errors.Is(cause, forwardFailure) {
		t.Errorf("cause %v lost the forwarding failure", cause)
	}
}

// Boundary: stopped before any signal arrives, the watch ends without forwarding or cancelling.
func TestCancelOnSignal_Boundary_StopBeforeAnySignal(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stopped := make(chan struct{})
	close(stopped)
	forward := func(os.Signal) error {
		t.Error("nothing was received, so nothing may be forwarded")
		return nil
	}
	cancelOnSignal(make(chan os.Signal), stopped, func() { t.Error("stopped twice") }, forward, cancel)
	if ctx.Err() != nil {
		t.Fatalf("the context was cancelled without a signal: %v", context.Cause(ctx))
	}
}

// Boundary: stop cancels without a signal cause, twice is harmless, and a cancelled parent
// cancels the copy with the parent's cause rather than a signal.
func TestCancelCommandsOnSignal_Boundary_StopAndParentCancelCarryNoSignal(t *testing.T) {
	ctx, stop := CancelCommandsOnSignal(context.Background())
	stop()
	stop()
	var interrupted *SignalError
	if !errors.Is(ctx.Err(), context.Canceled) || errors.As(context.Cause(ctx), &interrupted) {
		t.Fatalf("stop: err %v, cause %v; want canceled without a signal", ctx.Err(), context.Cause(ctx))
	}

	parent, cancelParent := context.WithCancelCause(context.Background())
	parentCause := errors.New("run deadline reached")
	ctx, stop = CancelCommandsOnSignal(parent)
	defer stop()
	cancelParent(parentCause)
	<-ctx.Done()
	if !errors.Is(context.Cause(ctx), parentCause) {
		t.Fatalf("cause = %v, want the parent's", context.Cause(ctx))
	}
}
