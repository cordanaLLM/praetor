// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package config

import (
	"strings"
	"testing"
)

// Order: engine-fr-rule-checks-20261027. Decode and validate through the canonical manifest.
func TestForgeText_Organizations(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"absent", "version: 1\n", true},
		{"own", "forge:\n  own_organizations: [Acme]\n", true},
		{"at_bound", "forge:\n  own_organizations: [" + strings.Repeat("acme,", 63) + "acme]\n", true},
		{"over_bound", "forge:\n  own_organizations: [" + strings.Repeat("acme,", 64) + "acme]\n", false},
		{"invalid_owner", "forge:\n  own_organizations: [a/b]\n", false},
		{"unknown_key", "forge:\n  own_organization: [acme]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest("test", []byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.name == "over_bound" && !strings.Contains(err.Error(), "exceeds 64 entries") {
				t.Fatal(err)
			}
			if !tc.valid && !strings.Contains(err.Error(), "own_organization") {
				t.Fatal(err)
			}
		})
	}
}
