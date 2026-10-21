// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

func TestValidatePRRaceReceiptScope(t *testing.T) {
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
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
			output := lockdown.GateOutputVersion + "\n" + lockdown.WorktreeCleanLine(true) + "\nrace_packages\t" + tc.scope +
				"\nstage\tRace-Detector Tests\t" + tc.status + "\t" + tc.reason + "\n"
			body := prChecklistBoxes + signedGateOutputBlock(t, priv, "abc", output)
			for _, policy := range []ReceiptPolicy{{PinnedKey: pub, HeadSHA: "abc"}, {HeadSHA: "abc"}} {
				result, err := ValidatePRChecklistWithPolicy(body, policy)
				if err != nil || result.Valid != tc.valid || result.HasReceipt != tc.valid {
					t.Fatalf("valid=%v result=%+v err=%v", tc.valid, result, err)
				}
			}
		})
	}
}
