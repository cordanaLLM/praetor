// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
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

// testValidContacts checks that each valid contact string loads and passes validation.
func testValidContacts(t *testing.T, contacts []string) {
	t.Helper()
	for _, contact := range contacts {
		body := "version: 1\nsecurity:\n  contact: \"" + contact + "\"\n"
		m, err := LoadManifest(writeManifest(t, body))
		if err != nil {
			t.Errorf("contact %q refused: %v", contact, err)
		}
		if got := m.Security.SecurityContact(); got != contact {
			t.Errorf("contact %q = %q", contact, got)
		}
		if !ValidSecurityContact(contact) {
			t.Errorf("ValidSecurityContact(%q) = false, want true", contact)
		}
	}
}

// testRefusedContacts checks that invalid or boundary contact strings are refused.
func testRefusedContacts(t *testing.T, contacts []string) {
	t.Helper()
	for _, contact := range contacts {
		body := "version: 1\nsecurity:\n  contact: \"" + contact + "\"\n"
		if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "security.contact") {
			t.Errorf("contact %q loaded: err = %v", contact, err)
		}
		if ValidSecurityContact(contact) {
			t.Errorf("ValidSecurityContact(%q) = true, want false", contact)
		}
	}
}

// TestSecurityPolicy_Contact validates declared security contacts: emails, mailto: URIs,
// and HTTPS URLs load; invalid forms are refused; nil policies return "".
func TestSecurityPolicy_Contact(t *testing.T) {
	var none *SecurityPolicy
	if none.SecurityContact() != "" {
		t.Errorf("nil policy contact = %q, want empty", none.SecurityContact())
	}
	testValidContacts(t, []string{
		"security@example.com",
		"mailto:security@example.com",
		"https://example.com/security",
		"https://example.com:8443/security",
	})
	testRefusedContacts(t, []string{
		"not-an-email",
		"http://insecure.example.com",
		"contact us privately",
	})
	testRefusedContacts(t, []string{
		"https:///report",
		"https://",
		"https:///",
		"https://:443/report",
	})
}

// channelManifest is a manifest declaring repository.forge and a security section body.
func channelManifest(forge, security string) string {
	m := "version: 1\n"
	if forge != "" {
		m += "repository:\n  forge: \"" + forge + "\"\n"
	}
	return m + "security:\n" + security
}

// Positive: each declarable security.channel loads on the forge it runs on, and contact on any.
func TestSecurityChannel_Positive_DeclaredChannelsLoad(t *testing.T) {
	cases := []struct{ forge, security string }{
		{"github", "  channel: private-reporting\n"},
		{"gitlab", "  channel: confidential-issues\n"},
		{"forgejo", "  channel: contact\n  contact: security@example.invalid\n"},
		{"", "  channel: private-reporting\n"},
	}
	for _, tc := range cases {
		m, err := LoadManifest(writeManifest(t, channelManifest(tc.forge, tc.security)))
		if err != nil {
			t.Fatalf("forge %q, %q: %v", tc.forge, tc.security, err)
		}
		choice, err := ResolveSecurityChannel(m.Security, m.Repository.Forge)
		if err != nil || !choice.Declared || choice.Channel != m.Security.Channel {
			t.Fatalf("ResolveSecurityChannel = %+v, %v", choice, err)
		}
	}
}

// Negative: an unknown channel, contact without security.contact and a forge-native channel on
// another declared forge fail the manifest load and name the key.
func TestSecurityChannel_Negative_BadDeclarationsFail(t *testing.T) {
	cases := []struct{ forge, security, want string }{
		{"github", "  channel: email\n", `security.channel "email" unsupported`},
		{"github", "  channel: contact\n", "security.channel contact requires security.contact"},
		{"gitlab", "  channel: private-reporting\n", "security.channel private-reporting runs on forge github, but repository.forge is gitlab"},
		{"forgejo", "  channel: confidential-issues\n", "security.channel confidential-issues runs on forge gitlab, but repository.forge is forgejo"},
	}
	for _, tc := range cases {
		_, err := LoadManifest(writeManifest(t, channelManifest(tc.forge, tc.security)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("forge %q, %q: error %v, want %q", tc.forge, tc.security, err, tc.want)
		}
	}
}

// Boundary: an undeclared channel takes the migration default (contact first, then the forge's
// native channel) and a forge without one, or none, wraps ErrSecurityChannelUndeclared; a nil
// section behaves as an empty one.
func TestSecurityChannel_Boundary_MigrationDefault(t *testing.T) {
	contact := &SecurityPolicy{Contact: "security@example.invalid"}
	cases := []struct {
		policy *SecurityPolicy
		forge  Forge
		want   SecurityChannel
	}{
		{nil, ForgeGitHub, SecurityChannelPrivateReporting},
		{&SecurityPolicy{}, ForgeGitLab, SecurityChannelConfidentialIssues},
		{contact, ForgeGitHub, SecurityChannelContact},
		{contact, "", SecurityChannelContact},
	}
	for _, tc := range cases {
		choice, err := ResolveSecurityChannel(tc.policy, tc.forge)
		if err != nil || choice.Declared || choice.Channel != tc.want || choice.Basis == "" {
			t.Fatalf("ResolveSecurityChannel(%+v, %q) = %+v, %v; want default %s", tc.policy, tc.forge, choice, err, tc.want)
		}
	}
	for _, forge := range []Forge{ForgeForgejo, ""} {
		if _, err := ResolveSecurityChannel(nil, forge); !errors.Is(err, ErrSecurityChannelUndeclared) {
			t.Fatalf("forge %q: error %v, want ErrSecurityChannelUndeclared", forge, err)
		}
	}
}
