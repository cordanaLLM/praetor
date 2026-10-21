// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

func TestGateVerifyRaceReceiptScope(t *testing.T) {
	for _, tc := range []struct {
		name, scope, status, reason string
		valid                       bool
	}{
		{"none", "none", "not_applicable", lockdown.NoAffectedGoPackage, true},
		{"scoped", "example.test/a", "passed", lockdown.RacePackagesReason([]string{"example.test/a"}), true},
		{"empty reason", "none", "not_applicable", "", false},
		{"missing list", "example.test/a", "passed", "tests passed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGateFixture(t)
			f.output = []byte(lockdown.GateOutputVersion + "\n" + lockdown.WorktreeCleanLine(true) + "\nrace_packages\t" + tc.scope +
				"\nstage\tRace-Detector Tests\t" + tc.status + "\t" + tc.reason + "\n")
			f.pin(t, f.pub)
			f.mintReceipt(t, f.priv, f.head)
			_, err := captureStdout(t, func() error { return runGate([]string{"verify", "--path=" + f.dir}) })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v verify=%v", tc.valid, err)
			}
		})
	}
}
