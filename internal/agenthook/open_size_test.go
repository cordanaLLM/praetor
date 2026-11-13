// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestStopLedgerOpenSize checks the stop hook below, at and above the 800 KiB warning and past the 1 MiB limit.
func TestStopLedgerOpenSize(t *testing.T) {
	for _, size := range []int{800*1024 - 1, 800 * 1024, 800*1024 + 1, 900 * 1024, 1<<20 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			verdict := stopLedgerSizeFixture(t, size)
			assertStopLedgerSize(t, verdict, size)
		})
	}
}

// stopLedgerSizeFixture writes an OPEN.md of exactly size bytes into a synced repository and returns the stop verdict.
func stopLedgerSizeFixture(t *testing.T, size int) Verdict {
	t.Helper()
	root := syncedRepository(t)
	body := strings.Repeat("padding\n", size/8) + strings.Repeat(" ", size%8)
	if err := os.WriteFile(filepath.Join(root, state.WorkingDirName, "OPEN.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if size <= 1<<20 {
		if _, err := state.SyncState(testsupport.DeadlineContext(t), root, "size fixture"); err != nil {
			t.Fatal(err)
		}
	}
	return evaluateStopLedger(testsupport.DeadlineContext(t), root, Canonical{})
}

// assertStopLedgerSize requires a warning above 800 KiB and a denial naming the archive command past 1 MiB.
func assertStopLedgerSize(t *testing.T, verdict Verdict, size int) {
	t.Helper()
	if size > 1<<20 {
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "praetorctl state task archive") {
			t.Fatalf("oversize stop: %+v", verdict)
		}
		return
	}
	if size <= 800*1024 {
		if verdict.Outcome != Allow || verdict.Notice != "" || verdict.Reason != "" {
			t.Fatalf("threshold stop: %+v", verdict)
		}
		return
	}
	merged := mergeStopChecks(verdict, Verdict{Outcome: Skip, Reason: "question unavailable"})
	dialect, _ := DialectFor("claude")
	response := dialect.Encode(Canonical{Event: EventStop}, merged)
	if response.ExitCode != 0 {
		t.Fatalf("size warning blocked stop: %+v", response)
	}
	for _, want := range []string{"warning", fmt.Sprint(size), "1048576", "praetorctl state task archive"} {
		if !strings.Contains(string(response.Stderr), want) {
			t.Errorf("stop warning lacks %q: %s", want, response.Stderr)
		}
	}
}
