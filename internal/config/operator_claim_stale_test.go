// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
	"time"
)

func TestOperatorClaimStale_Positive_SetAndDefault(t *testing.T) {
	policy, err := loadOperatorDocs(t, "forge: {claim_stale: 2h}", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.OperatorSettings().Forge.ClaimStaleWindow(); got != 2*time.Hour {
		t.Fatalf("window = %s, want 2h", got)
	}
	// A later layer replaces the earlier one.
	policy, err = loadOperatorDocs(t, "forge: {claim_stale: 2h}", "forge: {claim_stale: 90m}")
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.OperatorSettings().Forge.ClaimStaleWindow(); got != 90*time.Minute {
		t.Fatalf("workstation window = %s, want 90m", got)
	}
	if got := (ForgeSettings{}).ClaimStaleWindow(); got != DefaultClaimStale || DefaultClaimStale != 6*time.Hour {
		t.Fatalf("unset window = %s, want the 6h default", got)
	}
}

func TestOperatorClaimStale_Negative_Refused(t *testing.T) {
	for _, value := range []string{"soon", "0s", "-1h", "5m", "9m59s", "720h1s", "'6'"} {
		if _, err := loadOperatorDocs(t, "forge: {claim_stale: "+value+"}", ""); err == nil || !strings.Contains(err.Error(), "forge.claim_stale") {
			t.Errorf("claim_stale %s: err = %v, want a forge.claim_stale refusal", value, err)
		}
	}
}

func TestOperatorClaimStale_Boundary_Limits(t *testing.T) {
	for value, want := range map[string]time.Duration{"10m": 10 * time.Minute, "720h": 720 * time.Hour} {
		policy, err := loadOperatorDocs(t, "forge: {claim_stale: "+value+"}", "")
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got := policy.OperatorSettings().Forge.ClaimStaleWindow(); got != want {
			t.Fatalf("%s: window = %s", value, got)
		}
	}
}
