// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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
// held and then refused, and the command never runs.
func TestRunAdmit_Negative_UnderThresholdRefused(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return runAdmit([]string{"--min-available-gib", "1000000", "--wait", "1s", "--poll", "200ms", "--", "sh", "-c", "echo step-ran"})
	})
	if err == nil || !strings.Contains(err.Error(), "still held") || strings.Contains(out, "step-ran") {
		t.Fatalf("an under-threshold step was not refused: %v, output %q", err, out)
	}
}

// Boundary: the step's own exit status comes back as praetorctl's, and a passing step returns nil.
func TestRunAdmit_Boundary_ExitStatusPassesThrough(t *testing.T) {
	err := runAdmit([]string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--wait", "10s", "--",
		"sh", "-c", "exit 3"})
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("exit 3 came back as %v", err)
	}
	if err := runAdmit([]string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "true"}); err != nil {
		t.Fatalf("a passing step: %v", err)
	}
}
