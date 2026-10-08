// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
)

// Positive: an absent section and draft: fail keep the fail-closed step, draft: skip selects the
// job-level skip, and the selection survives RenderManifest, which adoption uses to rewrite the
// manifest.
func TestLoadManifestHostedGatesPositive(t *testing.T) {
	for section, want := range map[string]bool{"": false, "hosted_gates:\n  draft: fail\n": false, "hosted_gates:\n  draft: skip\n": true} {
		m, err := loadDocumentationManifest(t, section)
		if err != nil {
			t.Fatalf("%q: %v", section, err)
		}
		if got := m.HostedGates.DraftSkip(); got != want {
			t.Fatalf("%q: DraftSkip = %v, want %v", section, got, want)
		}
	}
	m, err := loadDocumentationManifest(t, "hosted_gates:\n  draft: skip\n")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil || !again.HostedGates.DraftSkip() {
		t.Fatalf("rendered manifest lost the opt-in: %v\n%s", err, rendered)
	}
}

// Negative: an unknown value, a second key, a non-string value, a repeated key and a non-mapping
// section are refused naming hosted_gates. Boundary: an explicit empty value is the default.
func TestLoadManifestHostedGatesNegative(t *testing.T) {
	for section, want := range map[string]string{
		"hosted_gates:\n  draft: allow\n":            `hosted_gates.draft "allow" must be "fail" (the default) or "skip"`,
		"hosted_gates:\n  draft: skip\n  other: x\n": "hosted_gates.other is not a known key",
		"hosted_gates:\n  draft: true\n":             "hosted_gates.draft must be a string",
		"hosted_gates: skip\n":                       "hosted_gates must be a mapping",
	} {
		_, err := loadDocumentationManifest(t, section)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: error = %v, want %q", section, err, want)
		}
	}
	if _, err := loadDocumentationManifest(t, "hosted_gates:\n  draft: skip\n  draft: fail\n"); err == nil {
		t.Fatal("a repeated draft key must be refused")
	}
	m, err := loadDocumentationManifest(t, "hosted_gates:\n  draft: \"\"\n")
	if err != nil || m.HostedGates.DraftSkip() {
		t.Fatalf("an empty draft value must keep the default: %v", err)
	}
}
