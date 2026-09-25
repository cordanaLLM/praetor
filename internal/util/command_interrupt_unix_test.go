//go:build unix

package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// interruptHelperRun selects TestCommandInterruptHelper in the re-executed test binary.
const interruptHelperRun = "-test.run=^TestCommandInterruptHelper$"

// interruptChildScript reports its start in ready, then writes marker only if it outlives
// a one-second pause. A marker present after the helper died is an orphaned command.
const interruptChildScript = "echo $$ > ready.tmp && mv ready.tmp ready && sleep 1 && touch marker"

// interruptPause is how long the parent test waits after the child reported ready before it
// looks for the marker: past the child's one-second pause, with slack for a loaded host.
const interruptPause = 2 * time.Second

// TestCommandInterruptHelper is the parent process of the interrupt tests. It is inert unless
// the environment selects a mode, and it runs one command through RunCommand in the
// directory the parent test chose.
func TestCommandInterruptHelper(t *testing.T) {
	mode := os.Getenv("PRAETOR_COMMAND_INTERRUPT_TEST")
	if mode == "" {
		return
	}
	switch mode {
	case "handled":
		TerminateCommandsOnSignal()
	case "ignored":
		signal.Ignore(syscall.SIGINT)
		TerminateCommandsOnSignal()
	case "unhandled":
	default:
		os.Exit(3)
	}
	dir := os.Getenv("PRAETOR_COMMAND_INTERRUPT_DIR")
	if _, err := RunCommand(context.Background(), dir, "sh", "-c", interruptChildScript); err != nil {
		os.Exit(9)
	}
	os.Exit(0)
}

// startInterruptHelper starts the helper in mode as the leader of its own process group, the
// way a shell starts a foreground job, and returns once its command reported ready.
func startInterruptHelper(t *testing.T, mode string) (*exec.Cmd, string, time.Time) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	helper := exec.Command(binary, interruptHelperRun)
	helper.Env = append(os.Environ(), "PRAETOR_COMMAND_INTERRUPT_TEST="+mode,
		"PRAETOR_COMMAND_INTERRUPT_DIR="+dir, "GOCOVERDIR="+t.TempDir())
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
	for i := 0; i < attempts; i++ {
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
// the way sig ends a process by default.
func signalHelper(t *testing.T, helper *exec.Cmd, sig syscall.Signal, group bool) {
	t.Helper()
	target := helper.Process.Pid
	if group {
		target = -target
	}
	if err := syscall.Kill(target, sig); err != nil {
		t.Fatal(err)
	}
	status := helperStatus(t, helper)
	if !status.Signaled() || status.Signal() != sig {
		t.Fatalf("helper did not die from %v: %v", sig, status)
	}
}

// The fixture must be able to fail: without the handler, the interrupt that ends the parent
// never reaches a command in its own process group, and the marker appears.
func TestCommandInterruptFixture_DetectsOrphanWithoutHandler(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "unhandled")
	signalHelper(t, helper, syscall.SIGINT, true)
	if !markerAfterPause(t, dir, ready) {
		t.Fatal("the fixture reported no orphan where one must exist")
	}
}

func TestTerminateCommandsOnSignal_Positive_GroupInterruptKillsCommand(t *testing.T) {
	t.Parallel()
	helper, dir, ready := startInterruptHelper(t, "handled")
	// A terminal's Ctrl-C signals the whole foreground process group.
	signalHelper(t, helper, syscall.SIGINT, true)
	if markerAfterPause(t, dir, ready) {
		t.Fatal("the command outlived its interrupted parent")
	}
}

func TestTerminateCommandsOnSignal_Positive_SignalToParentAloneKillsCommand(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			helper, dir, ready := startInterruptHelper(t, "handled")
			signalHelper(t, helper, sig, false)
			if markerAfterPause(t, dir, ready) {
				t.Fatalf("the command outlived a parent ended by %v", sig)
			}
		})
	}
}

// A process started with SIGINT ignored (nohup, a background job) keeps ignoring it, and
// its command runs to completion.
func TestTerminateCommandsOnSignal_Negative_IgnoredSignalStaysIgnored(t *testing.T) {
	t.Parallel()
	helper, dir, _ := startInterruptHelper(t, "ignored")
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

// ownedDescendant starts `sh -c "(sleep 0.3; touch leaked) & wait"` in dir through registry
// r, in a process group of its own as runBoundedCommand starts it. The file appears only if
// the grandchild outlives a kill of the group.
func ownedDescendant(t *testing.T, r *commandGroupRegistry, dir string) (*exec.Cmd, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "(sleep 0.3; touch leaked) & wait")
	cmd.Dir = dir
	// Only the process-group setup is wanted; the registry under test is r, not the
	// process-wide one the returned start and cleanup use.
	commandBytesCleanup(cmd)
	return cmd, r.start(cmd)
}

func TestCommandGroupRegistry_Boundary_TerminateWithoutCommandsRefusesLaterStarts(t *testing.T) {
	r := newCommandGroupRegistry()
	unlock, err := r.terminate()
	unlock()
	if err != nil {
		t.Fatalf("terminating no commands failed: %v", err)
	}
	cmd, err := ownedDescendant(t, r, t.TempDir())
	if !errors.Is(err, errCommandsTerminated) || cmd.Process != nil {
		t.Fatalf("a start after terminate was not refused: %v", err)
	}
	r.release(1) // releasing a group that was never recorded changes nothing
	if len(r.groups) != 0 {
		t.Fatalf("registry recorded %v", r.groups)
	}
}

func TestCommandGroupRegistry_Positive_TerminateKillsRunningGroup(t *testing.T) {
	r := newCommandGroupRegistry()
	dir := t.TempDir()
	cmd, err := ownedDescendant(t, r, dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := r.terminate()
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if waitErr := cmd.Wait(); !errors.As(waitErr, &exit) {
		t.Fatalf("the command was not killed: %v", waitErr)
	}
	r.release(cmd.Process.Pid)
	if len(r.groups) != 0 {
		t.Fatalf("a released group is still recorded: %v", r.groups)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("a grandchild survived the group kill: %v", err)
	}
}
