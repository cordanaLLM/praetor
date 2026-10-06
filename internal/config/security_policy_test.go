// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// goVEXManifest is a manifest declaring security.go_vex as document.
func goVEXManifest(document string) string {
	return "version: 1\nsecurity:\n  go_vex: \"" + document + "\"\n"
}

// Positive: a declared security.go_vex loads, and RepositoryGoVEXPath returns it for the
// repository whose manifest declares it.
func TestSecurityPolicy_Positive_DeclaredGoVEXPath(t *testing.T) {
	path := writeManifest(t, goVEXManifest(".config/security/go.openvex.json"))
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.Security.GoVEXPath(); got != ".config/security/go.openvex.json" {
		t.Fatalf("GoVEXPath = %q", got)
	}
	got, err := RepositoryGoVEXPath(filepath.Dir(path))
	if err != nil || got != ".config/security/go.openvex.json" {
		t.Fatalf("RepositoryGoVEXPath = %q, %v", got, err)
	}
}

// Negative: a path that is absolute, escapes the repository, uses a backslash or is not clean is
// refused, an unknown key under security fails the strict decode, and RepositoryGoVEXPath reports
// a manifest that does not load instead of falling back to the default document.
func TestSecurityPolicy_Negative_BadDeclarationsFail(t *testing.T) {
	// The manifest double-quotes the value, so `\\` decodes to the one backslash under test.
	for _, document := range []string{"/etc/vex.json", "../vex.json", `security\\vex.json`, "security/./vex.json", ".", ".."} {
		if _, err := LoadManifest(writeManifest(t, goVEXManifest(document))); err == nil ||
			!strings.Contains(err.Error(), "security.go_vex") {
			t.Errorf("document %q: err = %v, want the go_vex path refused", document, err)
		}
	}
	unknown := writeManifest(t, "version: 1\nsecurity:\n  go_vex_ignore: [GO-2022-1059]\n")
	if _, err := LoadManifest(unknown); err == nil {
		t.Error("an unknown security key loaded")
	}
	if got, err := RepositoryGoVEXPath(filepath.Dir(unknown)); err == nil || got != "" {
		t.Errorf("RepositoryGoVEXPath of an invalid manifest = %q, %v; want an error", got, err)
	}
}

// Boundary: no manifest, no section, an empty section and an empty value all name
// DefaultGoVEXPath, as does a nil policy; a path of exactly maxRepositoryPath bytes loads and one
// byte more is refused.
func TestSecurityPolicy_Boundary_DefaultsAndLengthLimit(t *testing.T) {
	var none *SecurityPolicy
	if none.GoVEXPath() != DefaultGoVEXPath {
		t.Errorf("nil policy names %q", none.GoVEXPath())
	}
	if got, err := RepositoryGoVEXPath(t.TempDir()); err != nil || got != DefaultGoVEXPath {
		t.Errorf("no manifest: %q, %v", got, err)
	}
	for _, body := range []string{"version: 1\n", "version: 1\nsecurity: {}\n", goVEXManifest("")} {
		got, err := RepositoryGoVEXPath(filepath.Dir(writeManifest(t, body)))
		if err != nil || got != DefaultGoVEXPath {
			t.Errorf("%q: %q, %v; want the default", body, got, err)
		}
	}
	longest := "vex/" + strings.Repeat("a", maxRepositoryPath-len("vex/.json")) + ".json"
	if _, err := LoadManifest(writeManifest(t, goVEXManifest(longest))); err != nil {
		t.Errorf("%d-byte path refused: %v", len(longest), err)
	}
	if _, err := LoadManifest(writeManifest(t, goVEXManifest("a"+longest))); err == nil {
		t.Errorf("%d-byte path loaded", len(longest)+1)
	}
}
