// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package lockdown

import (
	"strings"
	"testing"
)

// TestReceiptRaceStageStatus_Positive reads statuses without imposing the reuse-only passed policy.
func TestReceiptRaceStageStatus_Positive(t *testing.T) {
	for _, status := range []string{"passed", "skipped", "not_applicable"} {
		for _, scope := range []string{"", "race_packages\t./...\n"} {
			output := GateOutputVersion + "\n" + scope + "stage\t" + RaceStageName + "\t" + status +
				"\trace packages: ./...; affected Go packages supplied explicitly\n"
			got, err := ReceiptRaceStageStatus(output)
			if err != nil || got != status {
				t.Errorf("scope %q: status=%q, error=%v; want %q", scope, got, err, status)
			}
		}
	}
}

// TestReceiptRaceStageStatus_Negative refuses malformed, duplicated and inconsistent evidence.
func TestReceiptRaceStageStatus_Negative(t *testing.T) {
	stage := "stage\t" + RaceStageName + "\tpassed\tfixture\n"
	for _, output := range []string{
		stage + stage,
		"stage\t" + RaceStageName + "\tpassed",
		"race_packages\tnone\n",
		"race_packages\t../outside\n" + stage,
		stage + "race_packages\t./...\n",
		"race_packages\t./...\nrace_packages\t./...\n" + stage,
		"race_packages\texample.test/a\n" + stage,
		"stage\t" + RaceStageName + "\tnot_applicable\t \n",
	} {
		if got, err := ReceiptRaceStageStatus(output); err == nil || got != "" {
			t.Errorf("output %q: status=%q, error=%v; want empty status and error", output, got, err)
		}
	}
}

// TestReceiptVerificationPreservesUnscopedRaceStatuses keeps the stricter passed policy confined to reuse.
func TestReceiptVerificationPreservesUnscopedRaceStatuses(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"passed", "skipped", "not_applicable"} {
		output := GateOutputVersion + "\n" + WorktreeCleanLine(true) + "\n" +
			"stage\t" + RaceStageName + "\t" + status + "\trace detector availability recorded\n"
		rf := signedEnvelope(t, priv, output)
		if err := VerifyPinnedReceiptFile(rf, pub); err != nil {
			t.Errorf("pinned status %q: %v", status, err)
		}
		if err := VerifyUnpinnedReceiptFile(rf); err != nil {
			t.Errorf("unpinned status %q: %v", status, err)
		}
	}
}

// TestReceiptRaceStageStatus_Boundary preserves absent stages and enforces the shared 256-line bound.
func TestReceiptRaceStageStatus_Boundary(t *testing.T) {
	for _, output := range []string{"", GateOutputVersion, "stage\tOther\tpassed\tfixture"} {
		if got, err := ReceiptRaceStageStatus(output); err != nil || got != "" {
			t.Errorf("absent race stage: status=%q, error=%v", got, err)
		}
	}
	stage := "stage\t" + RaceStageName + "\tpassed\tfixture\twith tab"
	output := strings.Repeat("\n", 255) + stage
	if got, err := ReceiptRaceStageStatus(output); err != nil || got != "passed" {
		t.Fatalf("256 lines: status=%q, error=%v", got, err)
	}
	if _, err := ReceiptRaceStageStatus(output + "\n"); err == nil {
		t.Fatal("257 lines accepted")
	}
}

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
