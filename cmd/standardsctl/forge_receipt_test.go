// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// TestForgeReceiptRaceScopeRequiresPassed covers executed, skipped and absent race stages for reuse.
func TestForgeReceiptRaceScopeRequiresPassed(t *testing.T) {
	for _, selection := range []string{"", "./...", "example.test/a"} {
		for _, status := range []string{"passed", "skipped", "not_applicable", ""} {
			t.Run(selection+"/"+status, func(t *testing.T) {
				assertForgeReceiptRaceStatus(t, selection, status)
			})
		}
	}
}

// assertForgeReceiptRaceStatus checks a certified status against affected and empty push scopes.
func assertForgeReceiptRaceStatus(t *testing.T, selection, status string) {
	t.Helper()
	output := lockdown.GateOutputVersion + "\n" + lockdown.WorktreeCleanLine(true) + "\n"
	if selection != "" {
		output += "race_packages\t" + selection + "\n"
	}
	if status != "" {
		output += "stage\t" + lockdown.RaceStageName + "\t" + status + "\t" +
			lockdown.RacePackagesReason([]string{selection}) + "\n"
	}
	for _, scope := range []string{"example.test/a", "none"} {
		// Preserve existing scope matching for a push with no Go packages.
		valid := status == "passed"
		if scope == "none" {
			valid = selection != "example.test/a"
		}
		if err := verifyReceiptRaceScope(output, scope); (err == nil) != valid {
			t.Errorf("scope %q: valid=%v, error=%v", scope, valid, err)
		}
	}
}

// TestForgeReceiptRaceScopeMismatch refuses invalid scopes and unrelated certified packages.
func TestForgeReceiptRaceScopeMismatch(t *testing.T) {
	output := lockdown.GateOutputVersion + "\nrace_packages\texample.test/b\n" +
		"stage\t" + lockdown.RaceStageName + "\tpassed\t" + lockdown.RacePackagesReason([]string{"example.test/b"}) + "\n"
	for _, scope := range []string{"", "../outside", "example.test/a", "none"} {
		if err := verifyReceiptRaceScope(output, scope); err == nil {
			t.Errorf("scope %q accepted", scope)
		}
	}
}
