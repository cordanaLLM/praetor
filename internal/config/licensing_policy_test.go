// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
)

// TestLicensingPolicy_Validation tests validation of licensing section.
func TestLicensingPolicy_Validation(t *testing.T) {
	// Positive: valid holder string
	valid := "version: 1\nlicensing:\n  copyright_holder: \"example-holder <holder@example.invalid>\"\n"
	m, err := LoadManifest(writeManifest(t, valid))
	if err != nil {
		t.Fatalf("LoadManifest valid licensing = %v", err)
	}
	if got := m.DeclaredCopyrightHolder(); got != "example-holder <holder@example.invalid>" {
		t.Errorf("DeclaredCopyrightHolder() = %q, want %q", got, "example-holder <holder@example.invalid>")
	}

	// Boundary: exactly MaxCopyrightHolderBytes
	exact := "version: 1\nlicensing:\n  copyright_holder: \"" + strings.Repeat("a", MaxCopyrightHolderBytes) + "\"\n"
	if _, err := LoadManifest(writeManifest(t, exact)); err != nil {
		t.Fatalf("LoadManifest boundary licensing = %v", err)
	}

	// Negative: exceeds MaxCopyrightHolderBytes
	tooLong := "version: 1\nlicensing:\n  copyright_holder: \"" + strings.Repeat("a", MaxCopyrightHolderBytes+1) + "\"\n"
	if _, err := LoadManifest(writeManifest(t, tooLong)); err == nil {
		t.Fatal("LoadManifest with too long copyright_holder should fail")
	}

	// Negative: contains newline
	withNewline := "version: 1\nlicensing:\n  copyright_holder: \"line1\\nline2\"\n"
	if _, err := LoadManifest(writeManifest(t, withNewline)); err == nil {
		t.Fatal("LoadManifest with newline in copyright_holder should fail")
	}

	// Positive: absent licensing section
	absent := "version: 1\n"
	mAbsent, err := LoadManifest(writeManifest(t, absent))
	if err != nil {
		t.Fatalf("LoadManifest absent licensing = %v", err)
	}
	if got := mAbsent.DeclaredCopyrightHolder(); got != "" {
		t.Errorf("DeclaredCopyrightHolder() absent = %q, want empty", got)
	}

	// Nil safety
	var nilManifest *Manifest
	if got := nilManifest.DeclaredCopyrightHolder(); got != "" {
		t.Errorf("DeclaredCopyrightHolder() nil = %q, want empty", got)
	}
}
