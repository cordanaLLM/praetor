// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
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

// Positive, negative and boundary (#857): LoadRepositoryManifest is the one confined, validating
// reader of a repository's manifest. No manifest is (nil, nil); a valid one loads; an unknown key
// and an invalid hosted_gates.draft (ParseManifest validation, which a bare decode misses) are
// errors.
func TestLoadRepositoryManifest(t *testing.T) {
	root := t.TempDir()
	if manifest, err := LoadRepositoryManifest(t.Context(), root); manifest != nil || err != nil {
		t.Fatalf("no manifest = (%v, %v), want (nil, nil)", manifest, err)
	}
	path := filepath.Join(root, ManifestFileName)
	if err := os.WriteFile(path, []byte("hosted_gates:\n  draft: skip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if manifest, err := LoadRepositoryManifest(t.Context(), root); err != nil || !manifest.HostedGates.DraftSkip() {
		t.Fatalf("a valid manifest = (%v, %v), want the skip policy", manifest, err)
	}
	for text, want := range map[string]string{
		"unknown_key: true\n":                 "unknown_key",
		"hosted_gates:\n  draft: sometimes\n": "hosted_gates.draft",
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRepositoryManifest(t.Context(), root); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: error = %v, want %q", text, err, want)
		}
	}
}

// Negative (#857): a manifest that is a symlink out of the repository is refused, and so is a nil
// context.
func TestLoadRepositoryManifestRefusesAnEscapeAndANilContext(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("hosted_gates:\n  draft: skip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ManifestFileName)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := LoadRepositoryManifest(t.Context(), root); err == nil {
		t.Fatal("a manifest symlinked out of the repository was read")
	}
	if _, err := LoadRepositoryManifest(nil, root); err == nil { //nolint:staticcheck // the nil context is the case under test
		t.Fatal("a nil context was accepted")
	}
}
