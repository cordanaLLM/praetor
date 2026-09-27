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
// commands.
//
// The first signal hands Ctrl-C back to its default action, so an operator who presses it again
// ends the process at once instead of waiting for that cleanup. A repeated hangup or termination
// request is absorbed instead: closing a terminal delivers SIGHUP twice, once from the shell to
// its jobs and again from the kernel when the shell exits, and ending the process on the second
// would skip the cleanup the first one started. That cleanup is bounded, so absorbing them
// cannot hang the process.
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
	watch := newSignalWatch(watched)
	go cancelOnSignal(watch.received, watch.stopped, watch.releaseInterrupt, forwardToCommands, cancel)
	return ctx, func() {
		watch.stop()
		cancel(nil)
	}
}

// signalWatch is the signal registration CancelCommandsOnSignal holds for one piece of work.
type signalWatch struct {
	received  chan os.Signal // every watched signal, until the first one arrives
	absorbed  chan os.Signal // repeats of the lingering signals after the first; never read
	lingering []os.Signal    // the watched signals other than os.Interrupt
	stopped   chan struct{}  // closed by stop
	mu        sync.Mutex
	done      bool
}

// newSignalWatch starts relaying watched to received.
func newSignalWatch(watched []os.Signal) *signalWatch {
	w := &signalWatch{
		received: make(chan os.Signal, 1), absorbed: make(chan os.Signal, 1), stopped: make(chan struct{}),
	}
	for _, sig := range watched {
		if sig != os.Interrupt {
			w.lingering = append(w.lingering, sig)
		}
	}
	signal.Notify(w.received, watched...)
	return w
}

// releaseInterrupt runs on the first signal. It registers the lingering signals with absorbed
// before it stops relaying to received, so no repeat falls into the gap to its default action,
// and os.Interrupt, relayed nowhere else, reverts to its default action. After stop it does
// nothing, so a late first signal cannot leave a registration behind.
func (w *signalWatch) releaseInterrupt() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	if len(w.lingering) > 0 { // signal.Notify with no signals would relay every signal
		signal.Notify(w.absorbed, w.lingering...)
	}
	signal.Stop(w.received)
}

// stop releases every signal the watch holds and ends its wait; a second call does nothing.
func (w *signalWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.done = true
	signal.Stop(w.received)
	signal.Stop(w.absorbed)
	close(w.stopped)
}

// cancelOnSignal waits for the first signal on received, or for stopped. On a signal it calls
// releaseInterrupt, forwards the signal through forward and cancels with the signal as the cause.
func cancelOnSignal(received <-chan os.Signal, stopped <-chan struct{}, releaseInterrupt func(),
	forward func(os.Signal) error, cancel context.CancelCauseFunc) {
	select {
	case sig := <-received:
		releaseInterrupt()
		var cause error = &SignalError{Signal: sig}
		if err := forward(sig); err != nil {
			cause = errors.Join(cause, err)
		}
		cancel(cause)
	case <-stopped:
	}
}
