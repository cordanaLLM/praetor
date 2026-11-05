// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAdoptSecurityPolicy_GitHub_Default verifies that adoption of a GitHub repository
// without a declared security.contact renders GitHub's private vulnerability reporting instructions.
func TestAdoptSecurityPolicy_GitHub_Default(t *testing.T) {
	repo := newTestRepo(t, "gh-widget")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if !strings.Contains(content, "Use GitHub's private vulnerability reporting") {
		t.Fatalf("expected GitHub reporting instructions, got:\n%s", content)
	}
}

// TestAdoptSecurityPolicy_GitLab_Default verifies that adoption of a GitLab repository
// without a declared security.contact renders GitLab's confidential issue instructions.
func TestAdoptSecurityPolicy_GitLab_Default(t *testing.T) {
	repo := newTestRepo(t, "gl-widget")
	writeOriginRemote(t, repo, "https://gitlab.com/acme/gl-widget.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  forge: gitlab\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if !strings.Contains(content, "Open a confidential issue in this repository") {
		t.Fatalf("expected GitLab confidential issue instructions, got:\n%s", content)
	}
}

// TestAdoptSecurityPolicy_Forgejo_NoContact_Refused proves that adopting a Forgejo repository
// with no supported private channel and no security.contact skips writing the policy and names the remedy.
func TestAdoptSecurityPolicy_Forgejo_NoContact_Refused(t *testing.T) {
	repo := newTestRepo(t, "fj-widget")
	writeOriginRemote(t, repo, "https://codeberg.org/acme/fj-widget.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  forge: forgejo\n")
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(repo, securityFile)) {
		t.Fatal("no security policy may be written on Forgejo without a security contact")
	}
	if !reportNames(report, securityFile, "has no supported private vulnerability reporting channel") {
		t.Fatalf("expected report to name missing channel/contact: %+v", report.ActionDetails)
	}
}

// TestAdoptSecurityPolicy_Forgejo_WithContact proves that declaring security.contact
// enables successful adoption and renders the configured contact instructions.
func TestAdoptSecurityPolicy_Forgejo_WithContact(t *testing.T) {
	repo := newTestRepo(t, "fj-widget-contact")
	writeOriginRemote(t, repo, "https://codeberg.org/acme/fj-widget-contact.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  forge: forgejo\nsecurity:\n  contact: \"security@example.com\"\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if !strings.Contains(content, "Report vulnerabilities to the security contact: security@example.com") {
		t.Fatalf("expected security contact instructions, got:\n%s", content)
	}
}

// TestAdoptSecurityPolicy_ConfiguredContact_OverridesDefault proves that declaring security.contact
// on GitHub renders the contact instruction rather than GitHub private vulnerability reporting.
func TestAdoptSecurityPolicy_ConfiguredContact_OverridesDefault(t *testing.T) {
	repo := newTestRepo(t, "gh-widget-contact")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nsecurity:\n  contact: \"https://example.com/security\"\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if !strings.Contains(content, "Report vulnerabilities to the security contact: https://example.com/security") {
		t.Fatalf("expected overridden security contact instructions, got:\n%s", content)
	}
}
