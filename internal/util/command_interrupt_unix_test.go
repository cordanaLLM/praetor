//go:build unix

// These tests signal process groups, which exist only on Unix; on Windows commands stay in the
// console's group and TerminateCommandsOnSignal is a no-op (command_bytes_other.go).

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// interruptHelperRun selects TestCommandInterruptHelper in the re-executed test binary.
const interruptHelperRun = "-test.run=^TestCommandInterruptHelper$"

// readyStep reports the command's start, and its pid, in ready.
const readyStep = "echo $$ > ready.tmp && mv ready.tmp ready"

// interruptChildScript reports its start in ready, then writes marker only if it outlives
// a one-second pause. A marker present after the helper died is an orphaned command. It exits
// on a terminating signal through a trap: without one, a shell that receives SIGINT while the
// child it waits for (mv) has already exited assumes that child handled the signal and goes
// on, so the marker would say nothing about whether the signal arrived.
const interruptChildScript = "trap 'exit 130' INT TERM HUP; " + readyStep + " && sleep 1 && touch marker"

// cleanupChildScript holds lock the way git holds index.lock, and on a terminating signal
// removes it in a handler that leaves cleaned as evidence it ran. It waits in the wait builtin,
// which runs a trap at once; a foreground sleep caught between fork and exec can miss the
// signal and hold the trap back for its whole run. The sleep's output goes to /dev/null so it
// cannot keep the caller waiting on the command's pipes.
const cleanupChildScript = "trap 'rm -f lock; touch cleaned; exit 130' INT TERM HUP; : > lock; " +
	readyStep + "; sleep 30 >/dev/null 2>&1 & wait $!"

// stubbornChildScript ignores the terminating signals, and so does the sleep it runs.
const stubbornChildScript = "trap '' INT TERM HUP; " + readyStep + "; sleep 30"

// interruptPause is how long the parent test waits after the child reported ready before it
// looks for the marker: past the child's one-second pause, with slack for a loaded host.
const interruptPause = 2 * time.Second

// TestCommandInterruptHelper is the parent process of the interrupt tests. It is inert unless
// the environment selects a mode, and it runs one command through RunCommand in the
// directory the parent test chose.
//
// The command gets no parent-death signal unless PRAETOR_COMMAND_PARENT_DEATH is set. That
// signal kills the command whenever the helper dies, so a forwarding test would pass on Linux
// even if the signal it tests never arrived; command_parent_death_linux_test.go tests it alone.
func TestCommandInterruptHelper(t *testing.T) {
	mode := os.Getenv("PRAETOR_COMMAND_INTERRUPT_TEST")
	if mode == "" {
		return
	}
	if os.Getenv("PRAETOR_COMMAND_PARENT_DEATH") == "" {
		runningCommandGroups.parentDeath = 0
	}
	switch mode {
	case "handled":
		TerminateCommandsOnSignal(os.Exit)
	case "ignored":
		signal.Ignore(syscall.SIGINT)
		TerminateCommandsOnSignal(os.Exit)
	case "unhandled":
	default:
		os.Exit(3)
	}
	dir := os.Getenv("PRAETOR_COMMAND_INTERRUPT_DIR")
	script := os.Getenv("PRAETOR_COMMAND_INTERRUPT_SCRIPT")
	if _, err := RunCommand(context.Background(), dir, "sh", "-c", script); err != nil {
		os.Exit(9)
	}
	os.Exit(0)
}

// startInterruptHelper starts the helper in mode, running script, as the leader of its own
// process group, the way a shell starts a foreground job, and returns once its command
// reported ready. extraEnv is added to the helper's environment.
func startInterruptHelper(t *testing.T, mode, script string, extraEnv ...string) (*exec.Cmd, string, time.Time) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	helper := exec.Command(binary, interruptHelperRun)
	helper.Env = append(os.Environ(), "PRAETOR_COMMAND_INTERRUPT_TEST="+mode,
		"PRAETOR_COMMAND_INTERRUPT_DIR="+dir, "PRAETOR_COMMAND_INTERRUPT_SCRIPT="+script,
		"GOCOVERDIR="+t.TempDir())
	helper.Env = append(helper.Env, extraEnv...)
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if helper.ProcessState == nil {
			if err := syscall.Kill(-helper.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Errorf("kill helper group: %v", err)
			}
			waitErr := helper.Wait()
			var exit *exec.ExitError
			if waitErr != nil && !errors.As(waitErr, &exit) {
				t.Errorf("reap helper: %v", waitErr)
			}
		}
	})
	return helper, dir, waitForReady(t, filepath.Join(dir, "ready"))
}

// waitForReady polls for path within a bound generous enough for a re-executed race binary.
func waitForReady(t *testing.T, path string) time.Time {
	t.Helper()
	const attempts = 600
	for range attempts {
		if _, err := os.Stat(path); err == nil {
			return time.Now()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the helper's command never reported ready at %s", path)
	return time.Time{}
}

// helperStatus waits for the helper and returns how it ended.
func helperStatus(t *testing.T, helper *exec.Cmd) syscall.WaitStatus {
	t.Helper()
	waitErr := helper.Wait()
	var exit *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exit) {
		t.Fatalf("wait for helper: %v", waitErr)
	}
	status, ok := helper.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("unexpected process state %T", helper.ProcessState.Sys())
	}
	return status
}

// markerAfterPause reports whether the helper's command wrote its marker once its pause has
// certainly elapsed.
func markerAfterPause(t *testing.T, dir string, ready time.Time) bool {
	t.Helper()
	time.Sleep(time.Until(ready.Add(interruptPause)))
	_, err := os.Stat(filepath.Join(dir, "marker"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// signalHelper sends sig to the helper, or to its whole group, and asserts the helper ended
// the way sig ends a process by default. It returns how long the helper took to end.
func signalHelper(t *testing.T, helper *exec.Cmd, sig syscall.Signal, group bool) time.Duration {
	t.Helper()
	target := helper.Process.Pid
	if group {
		target = -target
	}
	sent := time.Now()
	if err := syscall.Kill(target, sig); err != nil {
		t.Fatal(err)
	}
	status := helperStatus(t, helper)
	if !status.Signaled() || status.Signal() != sig {
		t.Fatalf("helper did not die from %v: %v", sig, status)
	}
	return time.Since(sent)
}

// assertCleanedUp asserts that the command in dir ran its signal handler: the lock it held is
// gone and the handler's evidence exists.
func assertCleanedUp(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "cleaned")); err != nil {
		t.Fatalf("the command's signal handler did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lock")); !os.IsNotExist(err) {
		t.Fatalf("the command's lock survived it: %v", err)
	}
}

// awaitGroupGone waits, within a bound, until the process group led by the pid that the
// command in dir reported in ready has no member left.
func awaitGroupGone(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 200
	for range attempts {
		if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process group %d outlived its parent", pid)
}

// The fixture must be able to fail: without the handler, the interrupt that ends the parent
// never reaches a command in its own process group, and the marker appears.
func TestCommandInterruptFixture_DetectsOrphanWithoutHandler(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "unhandled", interruptChildScript)
	signalHelper(t, helper, syscall.SIGINT, true)
	if !markerAfterPause(t, dir, ready) {
		t.Fatal("the fixture reported no orphan where one must exist")
	}
}

func TestTerminateCommandsOnSignal_Positive_GroupInterruptEndsCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript)
	// A terminal's Ctrl-C signals the whole foreground process group.
	signalHelper(t, helper, syscall.SIGINT, true)
	if markerAfterPause(t, dir, ready) {
		t.Fatal("the command outlived its interrupted parent")
	}
}

func TestTerminateCommandsOnSignal_Positive_SignalToParentAloneEndsCommand(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			helper, dir, ready := startInterruptHelper(t, "handled", interruptChildScript)
			signalHelper(t, helper, sig, false)
			if markerAfterPause(t, dir, ready) {
				t.Fatalf("the command outlived a parent ended by %v", sig)
			}
		})
	}
}

// The command receives the signal that ends the parent, as it did in the parent's own process
// group, so it removes its lock the way git removes index.lock, instead of being killed with
// the lock left behind. The parent waits for the command, not for the whole grace.
func TestTerminateCommandsOnSignal_Positive_CommandRunsItsOwnCleanup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sig   syscall.Signal
		group bool
	}{{syscall.SIGINT, true}, {syscall.SIGTERM, false}, {syscall.SIGHUP, false}}
	for _, c := range cases {
		t.Run(c.sig.String(), func(t *testing.T) {
			t.Parallel()
			helper, dir, _ := startInterruptHelper(t, "handled", cleanupChildScript)
			if took := signalHelper(t, helper, c.sig, c.group); took >= CommandWaitDelay {
				t.Fatalf("the parent took %v to end although its command stopped at once", took)
			}
			assertCleanedUp(t, dir)
		})
	}
}

// A command that ignores the forwarded signal gets the whole grace, then is killed with its
// group, and the parent still ends the way the signal ends it.
func TestTerminateCommandsOnSignal_Negative_StubbornCommandKilledAfterGrace(t *testing.T) {
	t.Parallel()
	helper, dir, _ := startInterruptHelper(t, "handled", stubbornChildScript)
	if took := signalHelper(t, helper, syscall.SIGINT, true); took < CommandWaitDelay {
		t.Fatalf("the command was killed after %v, before its %v grace", took, CommandWaitDelay)
	}
	awaitGroupGone(t, dir)
}

// A process started with SIGINT ignored (nohup, a background job) keeps ignoring it, and
// its command runs to completion.
func TestTerminateCommandsOnSignal_Negative_IgnoredSignalStaysIgnored(t *testing.T) {
	t.Parallel()
	helper, dir, _ := startInterruptHelper(t, "ignored", interruptChildScript)
	if err := syscall.Kill(-helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	status := helperStatus(t, helper)
	if !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("an ignored SIGINT ended the helper: %v", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
		t.Fatalf("the command did not run to completion: %v", err)
	}
}

// startTracked starts `sh -c script` in dir through registry r and waits for it in the
// background with r's cleanup, as runBoundedCommand does. The channel yields how the command
// ended once its cleanup ran.
func startTracked(t *testing.T, r *commandGroupRegistry, dir, script string) (*exec.Cmd, <-chan syscall.WaitStatus, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", script)
	cmd.Dir = dir
	start, cleanup := r.track(cmd)
	if err := start(); err != nil {
		return cmd, nil, err
	}
	ended := make(chan syscall.WaitStatus, 1)
	go func() {
		var exit *exec.ExitError
		if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
			t.Errorf("wait for tracked command: %v", err)
		}
		if err := cleanup(); err != nil {
			t.Errorf("clean up tracked command: %v", err)
		}
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok {
			t.Errorf("unexpected process state %T", cmd.ProcessState.Sys())
		}
		ended <- status
	}()
	return cmd, ended, nil
}

// endedStatus waits, within a bound, for a tracked command to end.
func endedStatus(t *testing.T, ended <-chan syscall.WaitStatus) syscall.WaitStatus {
	t.Helper()
	select {
	case status := <-ended:
		return status
	case <-time.After(time.Minute):
		t.Fatal("the tracked command never ended")
		return 0
	}
}

// closedChannel is a grace that is already over.
func closedChannel() <-chan struct{} {
	over := make(chan struct{})
	close(over)
	return over
}

func TestCommandGroupRegistry_Positive_ForwardedSignalRunsCommandCleanup(t *testing.T) {
	r := newCommandGroupRegistry()
	dir := t.TempDir()
	if _, _, err := startTracked(t, r, dir, cleanupChildScript); err != nil {
		t.Fatal(err)
	}
	waitForReady(t, filepath.Join(dir, "ready"))
	grace, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	unlock, err := r.terminate(syscall.SIGINT, grace.Done())
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if grace.Err() != nil {
		t.Fatal("terminate waited out the whole grace for a command that had returned")
	}
	assertCleanedUp(t, dir)
}

// A command that ignores the forwarded signal is killed with its whole group once the grace is
// over, and not before.
func TestCommandGroupRegistry_Negative_StubbornGroupKilledWhenGraceEnds(t *testing.T) {
	r := newCommandGroupRegistry()
	dir := t.TempDir()
	script := "trap '' INT TERM HUP; (sleep 1; touch leaked) & " + readyStep + "; sleep 30"
	_, ended, err := startTracked(t, r, dir, script)
	if err != nil {
		t.Fatal(err)
	}
	waitForReady(t, filepath.Join(dir, "ready"))
	const graceBound = 300 * time.Millisecond
	began := time.Now()
	grace, cancel := context.WithTimeout(t.Context(), graceBound)
	defer cancel()
	unlock, err := r.terminate(syscall.SIGINT, grace.Done())
	took := time.Since(began)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if took < graceBound {
		t.Fatalf("the group was killed after %v, before its %v grace ended", took, graceBound)
	}
	if status := endedStatus(t, ended); !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("the stubborn command was not killed: %v", status)
	}
	time.Sleep(1500 * time.Millisecond) // past the grandchild's pause
	if _, err := os.Stat(filepath.Join(dir, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("a grandchild survived the group kill: %v", err)
	}
}

// A command that the forwarded signal ended leaves no descendant behind, even one that ignored
// the signal: its cleanup kills the rest of its group before terminate counts it as returned.
func TestCommandGroupRegistry_Positive_ReturnedCommandLeavesNoDescendant(t *testing.T) {
	r := newCommandGroupRegistry()
	dir := t.TempDir()
	// The grandchild ignores the signal, so only the group kill can stop it.
	script := "trap 'exit 130' INT; (trap '' INT; sleep 0.5; touch leaked) & " + readyStep + "; wait"
	_, ended, err := startTracked(t, r, dir, script)
	if err != nil {
		t.Fatal(err)
	}
	waitForReady(t, filepath.Join(dir, "ready"))
	grace, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	unlock, err := r.terminate(syscall.SIGINT, grace.Done())
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if grace.Err() != nil {
		t.Fatal("terminate waited out the whole grace for a command that had returned")
	}
	if status := endedStatus(t, ended); !status.Exited() || status.ExitStatus() != 130 {
		t.Fatalf("the command did not end through its handler: %v", status)
	}
	time.Sleep(time.Second) // past the grandchild's pause
	if _, err := os.Stat(filepath.Join(dir, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("a grandchild outlived its returned command: %v", err)
	}
}

// Boundary: the grace is already over when terminate starts. A group that has already exited
// costs no error and no wait, and a running group is killed at once.
func TestCommandGroupRegistry_Boundary_GraceAlreadyOver(t *testing.T) {
	r := newCommandGroupRegistry()
	exited := exec.CommandContext(t.Context(), "sh", "-c", "exit 0")
	startExited, _ := r.track(exited)
	if err := startExited(); err != nil {
		t.Fatal(err)
	}
	// Reaped but still recorded: its group no longer exists when terminate signals it.
	if err := exited.Wait(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, ended, err := startTracked(t, r, dir, stubbornChildScript)
	if err != nil {
		t.Fatal(err)
	}
	waitForReady(t, filepath.Join(dir, "ready"))
	unlock, err := r.terminate(syscall.SIGINT, closedChannel())
	unlock()
	if err != nil {
		t.Fatalf("a group that had already exited was reported: %v", err)
	}
	if status := endedStatus(t, ended); !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("the running command was not killed: %v", status)
	}
}

func TestCommandGroupRegistry_Boundary_TerminateWithoutCommandsRefusesLaterStarts(t *testing.T) {
	r := newCommandGroupRegistry()
	unlock, err := r.terminate(syscall.SIGINT, closedChannel())
	unlock()
	if err != nil {
		t.Fatalf("terminating no commands failed: %v", err)
	}
	cmd, _, err := startTracked(t, r, t.TempDir(), "exit 0")
	if !errors.Is(err, errCommandsTerminated) || cmd.Process != nil {
		t.Fatalf("a start after terminate was not refused: %v", err)
	}
	r.release(1) // releasing a group that was never recorded changes nothing
	if len(r.groups) != 0 {
		t.Fatalf("registry recorded %v", r.groups)
	}
}

func TestCommandReturned_3D(t *testing.T) {
	running := make(chan struct{})
	// Positive: a command that returned while the grace runs is reported as returned.
	if !commandReturned(closedChannel(), running) {
		t.Fatal("a returned command was reported as running")
	}
	// Negative: a command still running when the grace ends is not.
	if commandReturned(running, closedChannel()) {
		t.Fatal("a running command was reported as returned")
	}
	// Boundary: returned and grace over at once -- the command counts as returned every time,
	// so an exactly elapsed grace never kills a finished command.
	const draws = 200
	for i := range draws {
		if !commandReturned(closedChannel(), closedChannel()) {
			t.Fatalf("draw %d reported a returned command as running", i)
		}
	}
}
