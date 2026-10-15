// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package contextopt

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The claim-file lock of platforms without flock (#827): what a waiting writer reports for
// each state another writer can leave the claim in.

// Negative: a claim that records no usable process id is reported as such, never as a pid.
func TestDirectoryLockHolder_Negative_ClaimWithoutProcessID(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "garbage": "not-a-pid\n", "zero": "0\n", "negative": "-7\n"} {
		t.Run(name, func(t *testing.T) {
			root, _ := pinnedDirectory(t)
			if err := root.WriteFile(directoryClaimName, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			want := "another process (claim " + directoryClaimName + " records no process id)"
			if got := directoryLockHolder(root); got != want {
				t.Fatalf("holder = %q, want %q", got, want)
			}
		})
	}
}

// Boundary: a claim gone by the time the holder is named, or one that cannot be read, is
// reported with the reason, never as a process.
func TestDirectoryLockHolder_Boundary_MissingOrUnreadableClaim(t *testing.T) {
	root, _ := pinnedDirectory(t)
	if got := directoryLockHolder(root); !strings.HasPrefix(got, "another process (") || strings.Contains(got, "process 0") {
		t.Fatalf("missing claim: holder = %q", got)
	}
	if err := root.Mkdir(directoryClaimName, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := directoryLockHolder(root); !strings.HasPrefix(got, "another process (") {
		t.Fatalf("unreadable claim: holder = %q", got)
	}
}

// Negative: a stale claim left by an interrupted writer is waited out for the budget, then
// fails naming the process it records and the budget; it is never taken over silently.
func TestLockDirectory_Negative_StaleClaimWaitsOutBudget(t *testing.T) {
	root, _ := pinnedDirectory(t)
	const stalePID = 424242
	if err := root.WriteFile(directoryClaimName, []byte(strconv.Itoa(stalePID)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := LockDirectory(budgetContext(t, 150*time.Millisecond), root)
	if err == nil {
		t.Fatal("stale claim was taken over")
	}
	if waited := time.Since(started); waited < 150*time.Millisecond {
		t.Fatalf("gave up after %s, before the 150ms budget", waited)
	}
	for _, want := range []string{"process " + strconv.Itoa(stalePID) + " (claim " + directoryClaimName + ")", "past the 150ms wait budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("busy error lacks %q: %v", want, err)
		}
	}
	if _, statErr := root.Stat(directoryClaimName); statErr != nil {
		t.Fatalf("stale claim removed by the waiting writer: %v", statErr)
	}
	if err := root.Remove(directoryClaimName); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
