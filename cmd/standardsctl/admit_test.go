// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/admission/ptytest"
	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

// needShell skips a test whose step is a POSIX shell command where no sh is on PATH (a Windows
// runner without Git's usr/bin); the admission logic itself is covered by internal/admission.
func needShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH on %s: %v", runtime.GOOS, err)
	}
}

// Positive: the flags set the policy, the limits and the bounds; the label defaults to the
// command's name.
func TestParseAdmit_Positive_FlagsAndDefaults(t *testing.T) {
	opts, err := parseAdmit([]string{"--memory-high", "8G", "--memory-max", "12G", "--memory-swap-max", "0", "--min-available-gib", "20",
		"--wait", "1m", "--", "make", "verify-all"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.label != "make" || opts.policy.MinAvailableBytes != 20<<30 || opts.limits.MemorySwapMax != "0" || opts.wait != time.Minute ||
		strings.Join(opts.argv, " ") != "make verify-all" {
		t.Fatalf("options %+v", opts)
	}
	// Past `--` a word that looks like a flag of admit belongs to the command.
	opts, err = parseAdmit([]string{"--label", "build", "--", "make", "--wait", "-j4"})
	if err != nil || opts.wait != 6*time.Hour || strings.Join(opts.argv, " ") != "make --wait -j4" {
		t.Fatalf("command flags after --: %+v, %v", opts, err)
	}
}

// Negative: no command, a non-positive bound, a threshold past the bound and a limit systemd
// would misread are each refused before anything runs.
func TestParseAdmit_Negative_Refused(t *testing.T) {
	for _, args := range [][]string{
		{"--label", "x"},
		{"--wait", "0s", "--", "true"},
		{"--min-available-gib", "99999999", "--", "true"},
		{"--memory-max", "1.5G", "--", "true"},
		{"--memory-swap-max", "lots", "--", "true"},
	} {
		if _, err := parseAdmit(args); err == nil {
			t.Errorf("parseAdmit(%q) accepted", args)
		}
	}
}

// Negative (planted): a heavy step asked to start under a threshold the host cannot meet is
// held and then refused, and the command never runs. Where the host has no memory source
// (internal/sentinel memory_other.go, macOS among them) admission admits unjudged by design, so
// there the step runs; the branch follows the same reading admission takes, not the system name.
func TestRunAdmit_Negative_UnderThresholdRefused(t *testing.T) {
	needShell(t)
	stats, readErr := admission.HostReader(".")()
	out, err := captureStdout(t, func() error {
		return runAdmit([]string{"--min-available-gib", "1000000", "--wait", "1s", "--poll", "200ms", "--", "sh", "-c", "echo step-ran"})
	})
	if readErr == nil && stats.RAMTotalBytes == 0 {
		if err != nil || !strings.Contains(out, "step-ran") {
			t.Fatalf("on %s memory is not measurable and the step is admitted unjudged: %v, output %q", runtime.GOOS, err, out)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "still held") || strings.Contains(out, "step-ran") {
		t.Fatalf("an under-threshold step was not refused: %v, output %q", err, out)
	}
}

// Boundary: a short child (true) passes, and a failing step's exit status comes back as praetorctl's.
func TestRunAdmit_Boundary_ExitStatusPassesThrough(t *testing.T) {
	needShell(t)
	if err := runAdmit([]string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--wait", "10s", "--",
		"sh", "-c", "true"}); err != nil {
		t.Fatalf("short true step: %v", err)
	}
	err := runAdmit([]string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--wait", "10s", "--",
		"sh", "-c", "exit 3"})
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("exit 3 came back as %v", err)
	}
	if err := runAdmit([]string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "exit 0"}); err != nil {
		t.Fatalf("a passing step: %v", err)
	}
}

// Negative (planted): --wait 2s on a held host refuses within 2s plus one poll with a non-zero
// exit and reports the real time waited rather than echoing --wait.
func TestRunAdmit_Negative_HeldWaitReportsElapsed(t *testing.T) {
	needShell(t)
	const waitBound, poll = 2 * time.Second, 600 * time.Millisecond
	started := time.Now()
	prevReader := admitHostReader
	admitHostReader = func() (sentinel.HostStats, error) {
		if time.Since(started) >= waitBound {
			time.Sleep(150 * time.Millisecond)
		}
		return sentinel.HostStats{RAMTotalBytes: 64 << 30, RAMFreeBytes: 4 << 30, SwapMeasured: true, SwapTotalBytes: 64 << 30, SwapFreeBytes: 32 << 30}, nil
	}
	t.Cleanup(func() { admitHostReader = prevReader })
	_, err := captureStderr(t, func() error {
		return runAdmit([]string{"--label", "held-cli", "--wait", "2s", "--poll", "600ms", "--", "sh", "-c", "true"})
	})
	elapsed := time.Since(started)
	if err == nil || commandExitCode(io.Discard, err) == 0 {
		t.Fatalf("held runAdmit succeeded: %v", err)
	}
	if elapsed < waitBound || elapsed > waitBound+poll {
		t.Fatalf("elapsed = %s, want within [%s, %s]: %v", elapsed, waitBound, waitBound+poll, err)
	}
	_, after, ok := strings.Cut(err.Error(), "still held after ")
	raw, _, okCut := strings.Cut(after, " (")
	reported, parseErr := time.ParseDuration(raw)
	if !ok || !okCut || parseErr != nil || (reported-elapsed).Abs() > 80*time.Millisecond {
		t.Fatalf("reported wait in %q does not match elapsed %s (err=%v)", err.Error(), elapsed, parseErr)
	}
}

// Negative (planted): a child that blocks reading stdin is killed at --timeout within the
// deadline plus CommandWaitDelay, exiting non-zero and naming the deadline, label and argv[0].
func TestRunAdmit_Negative_BlockingStdinKilledAtDeadline(t *testing.T) {
	needShell(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		if cerr := w.Close(); cerr != nil {
			t.Error(cerr)
		}
		if cerr := r.Close(); cerr != nil {
			t.Error(cerr)
		}
	})
	const deadline = 300 * time.Millisecond
	started := time.Now()
	stderr, runErr := captureStderr(t, func() error {
		return runAdmit([]string{"--label", "stdin-cli", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0",
			"--wait", "5s", "--timeout", "300ms", "--", "sh", "-c", "trap 'exit 143' TERM; read line"})
	})
	elapsed := time.Since(started)
	if runErr == nil || commandExitCode(io.Discard, runErr) == 0 || elapsed > deadline+util.CommandWaitDelay {
		t.Fatalf("runErr=%v code=%d elapsed=%s stderr=%q", runErr, commandExitCode(io.Discard, runErr), elapsed, stderr)
	}
	for _, text := range []string{runErr.Error(), stderr} {
		if !strings.Contains(text, "300ms") || !strings.Contains(text, "stdin-cli") || !strings.Contains(text, "sh") {
			t.Fatalf("missing deadline/label/argv[0] in %q", text)
		}
	}
}

// Negative (planted): running praetorctl admit inside a controlling PTY (as in an agy lane)
// does not stop the child on SIGTTOU/SIGTTIN when the child initializes readline/termios.
func TestRunAdmit_Negative_ControllingTTYDoesNotStop(t *testing.T) {
	t.Setenv("PRAETOR_ADMIT_TEST_PTY", "1")
	ptytest.Run(t, "TestAdmitPTYHelper")
}

// TestAdmitPTYHelper is the child process the controlling-terminal case re-executes; it does nothing unless PRAETOR_ADMIT_TEST_PTY is 1.
func TestAdmitPTYHelper(t *testing.T) {
	if os.Getenv("PRAETOR_ADMIT_TEST_PTY") != "1" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRAETOR_ADMIT_TEST_PTY_CHILD", "1")
	if err := runAdmit([]string{"--label", "pty-cli", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0",
		"--wait", "5s", "--timeout", "1500ms", "--", self, "-test.run=^TestAdmitPTYChild$"}); err != nil {
		t.Fatalf("runAdmit under PTY: %v", err)
	}
}

// TestAdmitPTYChild re-executes terminal setup in the admitted background process group.
func TestAdmitPTYChild(t *testing.T) {
	if os.Getenv("PRAETOR_ADMIT_TEST_PTY_CHILD") == "1" {
		ptytest.Check(t)
	}
}
