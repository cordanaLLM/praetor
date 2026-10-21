// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package lockdown

import "testing"

func TestReceiptRaceScopeNeedsReason(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, scope, status, reason string
		valid                       bool
	}{
		{"none", "none", "not_applicable", "no affected Go package (--race-packages=none)", true},
		{"scoped", "example.test/a", "passed", "race packages: example.test/a; affected Go packages supplied explicitly", true},
		{"full", "./...", "passed", "race packages: ./...; affected Go packages supplied explicitly", true},
		{"missing reason", "none", "not_applicable", "", false},
		{"wrong reason", "none", "not_applicable", "no tests today", false},
		{"missing packages", "example.test/a", "passed", "", false},
		{"mismatched packages", "example.test/a", "passed", "race packages: example.test/b", false},
		{"none passed", "none", "passed", "no affected Go package (--race-packages=none)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := GateOutputVersion + "\n" + WorktreeCleanLine(true) + "\nrace_packages\t" + tc.scope + "\n"
			output += "stage\tRace-Detector Tests\t" + tc.status + "\t" + tc.reason + "\n"
			rf := signedEnvelope(t, priv, output)
			if err := VerifyPinnedReceiptFile(rf, pub); (err == nil) != tc.valid {
				t.Errorf("pinned valid=%v: %v", tc.valid, err)
			}
			if err := VerifyUnpinnedReceiptFile(rf); (err == nil) != tc.valid {
				t.Errorf("unpinned valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestReceiptRaceScopeMalformedEvidence(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	header := GateOutputVersion + "\n" + WorktreeCleanLine(true) + "\n"
	stage := "stage\tRace-Detector Tests\tnot_applicable\t" + NoAffectedGoPackage + "\n"
	for _, tc := range []struct{ name, body string }{
		{"duplicate scope", "race_packages\tnone\nrace_packages\tnone\n" + stage},
		{"scope after stages", stage + "race_packages\tnone\n"},
		{"scope without stage", "race_packages\tnone\n"},
		{"duplicate stage", "race_packages\tnone\n" + stage + stage},
		{"empty scope", "race_packages\t\n" + stage},
		{"unscoped missing reason", "stage\tRace-Detector Tests\tnot_applicable\t \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyPinnedReceiptFile(signedEnvelope(t, priv, header+tc.body), pub); err == nil {
				t.Fatal("malformed race evidence accepted")
			}
		})
	}
}
