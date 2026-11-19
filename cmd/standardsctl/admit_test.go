// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
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
	t.Setenv(admission.SlotsDirEnv, filepath.Join(t.TempDir(), "slots"))
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

// Boundary: the step's own exit status comes back as praetorctl's, and a passing step returns nil.
func TestRunAdmit_Boundary_ExitStatusPassesThrough(t *testing.T) {
	t.Setenv(admission.SlotsDirEnv, filepath.Join(t.TempDir(), "slots"))
	needShell(t)
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

func TestParseAdmit_HeavyFlags(t *testing.T) {
	opts, err := parseAdmit([]string{"--", "true"})
	if err != nil || opts.slots.MaxHeavy != 3 || opts.slots.Poll != 5*time.Second {
		t.Fatalf("defaults: %+v %v", opts.slots, err)
	}
	opts, err = parseAdmit([]string{"--max-heavy", "0", "--slots-dir", "explicit", "--slot-poll", "2ms", "--", "true"})
	if err != nil || opts.slots.MaxHeavy != 0 || opts.slots.Dir != "explicit" || opts.slots.Poll != 2*time.Millisecond {
		t.Fatalf("flags: %+v %v", opts.slots, err)
	}
	for _, flag := range [][]string{{"--max-heavy", "-1"}, {"--max-heavy", "4097"}, {"--slot-poll", "0s"}, {"--slot-poll", "-1s"}} {
		if _, err := parseAdmit(append(flag, "--", "true")); err == nil {
			t.Errorf("invalid %q", flag)
		}
	}
}

func TestRunAdmit_HeavyDirectoryRefusal(t *testing.T) {
	if !admission.HeavyCountAvailable {
		t.Skipf("heavy-step count is not available on %s; admitted uncounted", runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), "missing", "slots")
	err := runAdmit([]string{"--slots-dir", dir, "--", "must-not-exist-command"})
	if err == nil || !strings.Contains(err.Error(), "slots directory") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("directory refusal: %v", err)
	}
}

func TestRunAdmit_HeavyWaitRefusal(t *testing.T) {
	if !admission.HeavyCountAvailable {
		t.Skipf("heavy-step count is not available on %s; admitted uncounted", runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), "slots")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	holder, err := (admission.Slots{Dir: dir, MaxHeavy: 1, Poll: time.Millisecond}).Acquire(ctx, "planted-holder", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := holder.Close(); err != nil {
			t.Error(err)
		}
	}()
	err = runAdmit([]string{"--slots-dir", dir, "--max-heavy", "1", "--wait", "5ms", "--slot-poll", "1ms", "--", "must-not-run"})
	if err == nil || !strings.Contains(err.Error(), "1 of 1 heavy steps running") || !strings.Contains(err.Error(), "planted-holder") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wait refusal: %v", err)
	}
}

func TestRunAdmit_HeavyExplicitZero(t *testing.T) {
	needShell(t)
	var out string
	var runErr error
	lines := captureStderrAdmit(t, func() {
		out, runErr = captureStdout(t, func() error {
			return runAdmit([]string{"--max-heavy", "0", "--slots-dir", filepath.Join(t.TempDir(), "missing", "slots"), "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "echo counted-off"})
		})
	})
	if runErr != nil || !strings.Contains(out, "counted-off") {
		t.Fatalf("explicit off: %s %v", out, runErr)
	}
	if !strings.Contains(lines, "heavy-step count disabled (--max-heavy 0); admitted uncounted") {
		t.Fatal(lines)
	}
}

func TestRunAdmit_HeavyLineTexts(t *testing.T) {
	needShell(t)
	var runErr error
	lines := captureStderrAdmit(t, func() {
		runErr = runAdmit([]string{"--slots-dir", filepath.Join(t.TempDir(), "slots"), "--label", "line-test", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "exit 0"})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(lines, "admitted line-test:") {
		t.Fatal(lines)
	}
	want := "slot 1 of 3"
	if !admission.HeavyCountAvailable {
		want = "heavy-step count is not available on " + runtime.GOOS + "; admitted uncounted"
	}
	if !strings.Contains(lines, want) {
		t.Fatal(lines)
	}
	if strings.Contains(lines, "runs unscoped") && !strings.Contains(lines, "concurrency: "+want) {
		t.Fatal(lines)
	}
}

func TestParseAdmit_HeavyHelp(t *testing.T) {
	out := captureStderrAdmit(t, func() {
		if _, err := parseAdmit([]string{"--help"}); err == nil {
			t.Error("help must stop parsing")
		}
	})
	for _, text := range []string{"max-heavy", "slot-poll", "slots-dir", "PRAETOR_ADMIT_SLOTS_DIR", "explicitly disables"} {
		if !strings.Contains(out, text) {
			t.Errorf("help missing %s: %s", text, out)
		}
	}
}

func captureStderrAdmit(t *testing.T, run func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = file
	defer func() {
		os.Stderr = original
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	run()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
