// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/govuln"
)

// Replay the concurrent-prune diagnostic from order engine-gate-private-gocache-20261025.
// A stub fixes the deletion window without pruning a shared host cache or relying on timing.
const securityCacheMiss = "could not import fmt (open /home/test/.cache/go-build/ab/entry-d: no such file or directory)"

func TestSecurityGovulnCacheMiss(t *testing.T) {
	cases := []struct {
		name, step, message            string
		failures, calls, status, notes int
	}{
		{"scanner recovers", "tool", securityCacheMiss, 1, 2, 0, 1},
		{"scanner fails twice", "tool", securityCacheMiss, 2, 2, 2, 1},
		{"scanner network", "tool", "network unavailable", 1, 1, 2, 0},
		{"list recovers", "list", "open /home/test/.cache/go-build/ab/entry-a: no such file or directory", 1, 2, 0, 1},
		{"list fails twice", "list", securityCacheMiss, 2, 2, 2, 1},
		{"list network", "list", "network unavailable", 1, 1, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkSecurityCacheRetry(t, tc.step, tc.message, tc.failures, tc.calls, tc.status, tc.notes)
		})
	}
}

func checkSecurityCacheRetry(t *testing.T, step, message string, failures, wantCalls, wantStatus, wantNotes int) {
	t.Helper()
	dir := t.TempDir()
	seedTrackedGovulnModule(t, dir)
	calls := 0
	run := securityCacheRunner(t, dir, step, message, failures, &calls)
	var stdout, stderr bytes.Buffer
	err := dispatchSecurity([]string{"govuln", "--path=" + dir, "--", "go", "tool", "govulncheck"}, run, &stdout, &stderr)
	notes := strings.Count(stdout.String(), "govuln: note: go-build cache miss, retried once: go.mod\n")
	if calls != wantCalls || exitCode(err) != wantStatus || notes != wantNotes {
		t.Fatalf("calls=%d exit=%d notes=%d; want %d/%d/%d; stdout=%q stderr=%q", calls, exitCode(err), notes,
			wantCalls, wantStatus, wantNotes, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "govuln: PASS:") != (wantStatus == 0) ||
		strings.Contains(stderr.String(), "no verdict") != (wantStatus == 2) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	t.Logf("calls=%d exit=%d notes=%d pass=%t", calls, exitCode(err), notes, wantStatus == 0)
}

func securityCacheRunner(t *testing.T, dir, step, message string, failures int, calls *int) govuln.Runner {
	t.Helper()
	return func(ctx context.Context, gotDir, name string, args ...string) (string, error) {
		if _, ok := ctx.Deadline(); !ok || gotDir != dir || name != "go" {
			t.Fatalf("command lost deadline, directory or executable: %s %s %v", gotDir, name, args)
		}
		if args[0] == step {
			*calls++
			if *calls <= failures {
				return "", errors.New(message)
			}
		}
		if args[0] == "list" {
			return "example.test/security\n", nil
		}
		return `{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scanner_version":"v1.8.0","scan_level":"symbol","scan_mode":"source"}}`, nil
	}
}

func TestSecurityGovulnCacheMissNoteOncePerModule(t *testing.T) {
	dir := t.TempDir()
	seedTrackedGovulnModule(t, dir)
	calls := make(map[string]int)
	run := func(_ context.Context, _, _ string, args ...string) (string, error) {
		calls[args[0]]++
		if calls[args[0]] == 1 {
			return securityCacheMiss, errors.New("exit status 1")
		}
		if args[0] == "list" {
			return "example.test/pkg", nil
		}
		return `{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scanner_version":"v1.8.0","scan_level":"symbol","scan_mode":"source"}}`, nil
	}
	var stdout, stderr bytes.Buffer
	err := dispatchSecurity([]string{"govuln", "--path=" + dir, "--", "go", "tool", "govulncheck"}, run, &stdout, &stderr)
	if err != nil || stderr.Len() != 0 || calls["list"] != 2 || calls["tool"] != 2 ||
		strings.Count(stdout.String(), "govuln: note: go-build cache miss, retried once: go.mod\n") != 1 {
		t.Fatalf("calls=%v err=%v stdout=%q stderr=%q", calls, err, stdout.String(), stderr.String())
	}
}
