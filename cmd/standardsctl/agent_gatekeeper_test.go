package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/util"
)

// stubGatekeeperPipeline replaces the gating pipeline for one test with one that returns rep
// and err, and returns the number of times the pipeline has been called.
func stubGatekeeperPipeline(t *testing.T, rep *gating.PipelineReport, err error) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	original := gatedPipeline
	gatedPipeline = func(context.Context, string, bool) (*gating.PipelineReport, error) {
		calls.Add(1)
		return rep, err
	}
	t.Cleanup(func() { gatedPipeline = original })
	return calls
}

// runGatekeeper runs `agent run praetor-gatekeeper` and returns its output, its error and the
// exit code main would give that error.
func runGatekeeper(t *testing.T) (out string, code int, err error) {
	t.Helper()
	out, err = captureStdout(t, func() error {
		return dispatchCommand("agent", []string{"run", "praetor-gatekeeper"})
	})
	if err == nil {
		return out, 0, nil
	}
	return out, commandExitCode(&bytes.Buffer{}, err), err
}

// Positive: an admitted pipeline exits 0 and prints the stage report and the verdict.
func TestGatekeeperAgent_Positive_AdmittedExitsZero(t *testing.T) {
	stubGatekeeperPipeline(t, &gating.PipelineReport{
		Status: gating.StatusAdmitted,
		Stages: []gating.StageResult{{Name: "Race-Detector Tests", Status: gating.StagePassed}},
	}, nil)
	out, code, err := runGatekeeper(t)
	if err != nil || code != 0 {
		t.Fatalf("admitted gatekeeper run: err %v, exit %d; want nil, 0", err, code)
	}
	mustContain(t, out, "Pipeline Result: ADMITTED", "[PASS] Race-Detector Tests",
		"[praetor-gatekeeper] Gated pipeline completed: ADMITTED")
}

// Negative: a rejected pipeline exits 1, as gate run does, instead of reporting the rejection
// under exit 0, and still prints the failing stage, its reason and the verdict first.
func TestGatekeeperAgent_Negative_RejectedExitsOneWithTheFailingStage(t *testing.T) {
	stubGatekeeperPipeline(t, &gating.PipelineReport{
		Status: gating.StatusRejected,
		Stages: []gating.StageResult{{Name: "HISS Invariant Scan", Status: gating.StageFailed, Message: "[HISS-04] main.go:12 - too long"}},
	}, nil)
	out, code, err := runGatekeeper(t)
	mustErrContain(t, err, "gatekeeper rejection")
	if code != 1 {
		t.Fatalf("rejected gatekeeper run exits %d, want 1", code)
	}
	mustContain(t, out, "Pipeline Result: REJECTED", "HISS Invariant Scan", "Reason: [HISS-04] main.go:12 - too long",
		"[praetor-gatekeeper] Gated pipeline completed: REJECTED")
}

// Negative: a pipeline that could not run is an execution error that keeps its cause.
func TestGatekeeperAgent_Negative_ExecutionErrorKeepsTheCause(t *testing.T) {
	cause := errors.New("resolve repository root: not a git repository")
	stubGatekeeperPipeline(t, nil, cause)
	out, code, err := runGatekeeper(t)
	mustErrContain(t, err, "gatekeeper execution error")
	if !errors.Is(err, cause) || code != 1 {
		t.Fatalf("execution error: err %v, exit %d; want the cause wrapped, exit 1", err, code)
	}
	if strings.Contains(out, "Gated pipeline completed") {
		t.Fatalf("a pipeline that never ran must not report a verdict: %s", out)
	}
}

// interruptedContext is a context a signal cancelled, as util.CancelCommandsOnSignal leaves it.
func interruptedContext(sig syscall.Signal) context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(&util.SignalError{Signal: sig})
	return ctx
}

// Positive: a failure after a signal cancelled the run exits with the shell's 128+signal status,
// so an interrupted gate run is not mistaken for a rejected one (exit 1).
func TestInterruptedError_Positive_SignalSetsTheExitStatus(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143, syscall.SIGHUP: 129} {
		err := interruptedError(interruptedContext(sig), errors.New("repository rejected by gating pipeline"))
		var stderr bytes.Buffer
		if code := commandExitCode(&stderr, err); code != want {
			t.Errorf("%v: exit %d, want %d", sig, code, want)
		}
		mustContain(t, stderr.String(), "repository rejected by gating pipeline", "interrupted by "+sig.String())
	}
}

// Negative: without a signal cause the error and its exit status are unchanged.
func TestInterruptedError_Negative_NoSignalKeepsTheError(t *testing.T) {
	rejected := errors.New("repository rejected by gating pipeline")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, parent := range []context.Context{context.Background(), ctx} {
		if err := interruptedError(parent, rejected); !errors.Is(err, rejected) || err.Error() != rejected.Error() {
			t.Fatalf("interruptedError changed %v into %v", rejected, err)
		}
	}
	if code := commandExitCode(&bytes.Buffer{}, rejected); code != 1 {
		t.Fatalf("a rejection exits %d, want 1", code)
	}
}

// Boundary: a command that succeeded despite the signal stays a success.
func TestInterruptedError_Boundary_SuccessAfterSignalStaysSuccess(t *testing.T) {
	if err := interruptedError(interruptedContext(syscall.SIGINT), nil); err != nil {
		t.Fatalf("a successful run became %v", err)
	}
}
