package util

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// SignalError is the cancellation cause CancelCommandsOnSignal records: the terminating signal
// that stopped the work. context.Cause returns it, alone or joined with a failure to forward
// the signal, so errors.As finds it either way.
type SignalError struct {
	Signal os.Signal
}

func (e *SignalError) Error() string {
	return "interrupted by " + e.Signal.String()
}

// ExitCode is the status a shell reports for a process the signal ended: 128 plus the signal's
// number, or 1 for a signal without one.
func (e *SignalError) ExitCode() int {
	if number, ok := e.Signal.(syscall.Signal); ok && number > 0 {
		return 128 + int(number)
	}
	return 1
}

// unignoredTerminatingSignals returns the terminating signals this process does not ignore. A
// signal the process already ignores stays ignored, as nohup and background jobs expect.
func unignoredTerminatingSignals() []os.Signal {
	watched := make([]os.Signal, 0, len(terminatingSignals))
	for _, sig := range terminatingSignals {
		if !signal.Ignored(sig) {
			watched = append(watched, sig)
		}
	}
	return watched
}

// CancelCommandsOnSignal returns a copy of parent that is cancelled when this process receives a
// terminating signal it does not ignore: SIGINT, SIGTERM and SIGHUP on Unix, Ctrl-C and a
// console close elsewhere. It is TerminateCommandsOnSignal for work that must clean up after
// itself: the same signals, the same respect for ignored ones, and on Unix the same forwarding
// to every running command group, so each command still sees the signal it would have seen in
// this process's own group. It then cancels the context, with a *SignalError cause, instead of
// ending the process, and never refuses a later command, so deferred cleanup that runs under
// context.WithoutCancel (removing an isolated worktree and its branch) still starts its git
// commands. The first signal restores the default action, so a second one ends the process at
// once instead of waiting for that cleanup.
//
// Install at most one of the two handlers in a process: TerminateCommandsOnSignal keeps the
// command registry locked once it fires, which would block this cleanup. stop releases the
// signals and cancels the context; call it when the work returns.
func CancelCommandsOnSignal(parent context.Context) (ctx context.Context, stop context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	watched := unignoredTerminatingSignals()
	if len(watched) == 0 {
		return ctx, func() { cancel(nil) }
	}
	received := make(chan os.Signal, 1)
	signal.Notify(received, watched...)
	stopped := make(chan struct{})
	var once sync.Once
	stopWatching := func() {
		once.Do(func() {
			signal.Stop(received)
			close(stopped)
		})
	}
	go cancelOnSignal(received, stopped, stopWatching, forwardToCommands, cancel)
	return ctx, func() {
		stopWatching()
		cancel(nil)
	}
}

// cancelOnSignal waits for the first signal on received, or for stopped. On a signal it stops
// watching, forwards the signal through forward and cancels with the signal as the cause.
func cancelOnSignal(received <-chan os.Signal, stopped <-chan struct{}, stopWatching func(),
	forward func(os.Signal) error, cancel context.CancelCauseFunc) {
	select {
	case sig := <-received:
		stopWatching()
		var cause error = &SignalError{Signal: sig}
		if err := forward(sig); err != nil {
			cause = errors.Join(cause, err)
		}
		cancel(cause)
	case <-stopped:
	}
}
