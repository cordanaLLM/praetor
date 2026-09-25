//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalProcessRun selects TestSignalProcessHelper in the re-executed test binary.
const signalProcessRun = "-test.run=^TestSignalProcessHelper$"

// stubGit stands in for git: it reports its start in ready, then writes marker only if it
// outlives a one-second pause. A marker present after praetorctl died is an orphaned command.
const stubGit = "#!/bin/sh\necho $$ > ready.tmp && mv ready.tmp ready && sleep 1 && touch marker\n"

// TestSignalProcessHelper is the child of the signal tests: it runs main with the arguments
// in PRAETOR_SIGNAL_PROCESS_TEST, as the praetorctl binary does.
func TestSignalProcessHelper(t *testing.T) {
	arguments := os.Getenv("PRAETOR_SIGNAL_PROCESS_TEST")
	if arguments == "" {
		return
	}
	os.Args = append([]string{"praetorctl"}, strings.Fields(arguments)...)
	main()
	os.Exit(0)
}

// A terminal's Ctrl-C reaches praetorctl but not the command it runs, which sits in a
// process group of its own. main must take that command down with it.
func TestMain_Positive_InterruptKillsRunningCommand(t *testing.T) {
	stubs, repo := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(stubs, "git"), []byte(stubGit), 0o700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(binary, signalProcessRun)
	helper.Env = append(os.Environ(),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRAETOR_SIGNAL_PROCESS_TEST=ci filter --dir "+repo, "GOCOVERDIR="+t.TempDir())
	// Its own group stands in for a shell's foreground job, so the group signal below
	// reaches praetorctl and not the test runner.
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if helper.ProcessState != nil {
			return
		}
		if err := syscall.Kill(-helper.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill helper group: %v", err)
		}
		var exit *exec.ExitError
		if err := helper.Wait(); err != nil && !errors.As(err, &exit) {
			t.Errorf("reap helper: %v", err)
		}
	})
	ready := waitForStubStart(t, filepath.Join(repo, "ready"))
	if err := syscall.Kill(-helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := helper.Wait(); !errors.As(err, &exit) {
		t.Fatalf("praetorctl survived the interrupt: %v", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Fatalf("praetorctl did not end as interrupted: %v", exit)
	}
	time.Sleep(time.Until(ready.Add(2 * time.Second)))
	if _, err := os.Stat(filepath.Join(repo, "marker")); !os.IsNotExist(err) {
		t.Fatalf("the git command outlived the interrupted praetorctl: %v", err)
	}
}

// waitForStubStart polls for path within a bound generous enough for a re-executed race
// binary.
func waitForStubStart(t *testing.T, path string) time.Time {
	t.Helper()
	const attempts = 600
	for i := 0; i < attempts; i++ {
		if _, err := os.Stat(path); err == nil {
			return time.Now()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the stub git never started: %s is absent", path)
	return time.Time{}
}
