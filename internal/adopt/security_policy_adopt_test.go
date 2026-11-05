// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdoptSecurityPolicy_GitHub_Default verifies that adoption of a GitHub repository
// without a declared security.contact renders GitHub's private vulnerability reporting instructions.
func TestAdoptSecurityPolicy_GitHub_Default(t *testing.T) {
	repo := newTestRepo(t, "gh-widget")
	writeOriginRemote(t, repo, "https://github.com/acme/gh-widget.git")
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
	if !reportNames(report, securityFile, "repository.forge") ||
		!reportNames(report, securityFile, "security.contact") ||
		!reportNames(report, securityFile, "decline security-policy") {
		t.Fatalf("expected report to name repository.forge, security.contact, and decline security-policy: %+v", report.ActionDetails)
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
	writeOriginRemote(t, repo, "https://github.com/acme/gh-widget-contact.git")
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

// TestAdoptSecurityPolicy_UndeclaredNonGitHubHost_Skip verifies that a repository
// on a non-github host without declared repository.forge skips scaffolding SECURITY.md
// and names repository.forge, security.contact, and decline security-policy.
func TestAdoptSecurityPolicy_UndeclaredNonGitHubHost_Skip(t *testing.T) {
	repo := newTestRepo(t, "nongh-undeclared")
	writeOriginRemote(t, repo, "https://gitlab.example.org/acme/nongh-undeclared.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(repo, securityFile)) {
		t.Fatal("no security policy may be written on undeclared forge without security.contact")
	}
	if !reportNames(report, securityFile, "repository.forge") ||
		!reportNames(report, securityFile, "security.contact") ||
		!reportNames(report, securityFile, "decline security-policy") {
		t.Fatalf("expected skip reason naming repository.forge, security.contact, and decline security-policy: %+v", report.ActionDetails)
	}
}

// TestAdoptSecurityPolicy_NoOriginRemote_Skip verifies that a repository without an
// origin remote and without declared repository.forge skips scaffolding SECURITY.md.
func TestAdoptSecurityPolicy_NoOriginRemote_Skip(t *testing.T) {
	repo := newTestRepo(t, "no-origin-skip")
	mustWrite(t, filepath.Join(repo, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(repo, securityFile)) {
		t.Fatal("no security policy may be written without an origin remote or declared forge")
	}
	if !reportNames(report, securityFile, "repository.forge") ||
		!reportNames(report, securityFile, "security.contact") ||
		!reportNames(report, securityFile, "decline security-policy") {
		t.Fatalf("expected skip reason naming repository.forge, security.contact, and decline security-policy: %+v", report.ActionDetails)
	}
}

// TestAdoptSecurityPolicy_DeclaredGitHub verifies that declaring repository.forge: github
// scaffolds GitHub private vulnerability reporting even without an origin remote.
func TestAdoptSecurityPolicy_DeclaredGitHub(t *testing.T) {
	repo := newTestRepo(t, "declared-github")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  forge: github\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if !strings.Contains(content, "Use GitHub's private vulnerability reporting") {
		t.Fatalf("expected GitHub reporting instructions, got:\n%s", content)
	}
}

// TestAdoptSecurityPolicy_DeclaredGitLab verifies that declaring repository.forge: gitlab
// scaffolds GitLab confidential issues policy.
func TestAdoptSecurityPolicy_DeclaredGitLab(t *testing.T) {
	repo := newTestRepo(t, "declared-gitlab")
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

// TestAdoptSecurityPolicy_ContactOnAnyForge verifies that declaring security.contact
// scaffolds the contact policy across declared and undeclared forges.
func TestAdoptSecurityPolicy_ContactOnAnyForge(t *testing.T) {
	forges := []struct {
		name, forge, host string
	}{
		{"github", "github", "https://github.com/acme/repo.git"},
		{"gitlab", "gitlab", "https://gitlab.com/acme/repo.git"},
		{"forgejo", "forgejo", "https://codeberg.org/acme/repo.git"},
		{"undeclared", "", "https://git.corp.example.com/acme/repo.git"},
	}
	for _, tc := range forges {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t, "contact-"+tc.name)
			writeOriginRemote(t, repo, tc.host)
			mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
			manifest := "version: 1\nsecurity:\n  contact: \"security@corp.example.com\"\n"
			if tc.forge != "" {
				manifest = "version: 1\nrepository:\n  forge: " + tc.forge + "\nsecurity:\n  contact: \"security@corp.example.com\"\n"
			}
			mustWrite(t, filepath.Join(repo, manifestFile), manifest)
			if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
				t.Fatal(err)
			}
			content := mustRead(t, filepath.Join(repo, securityFile))
			if !strings.Contains(content, "Report vulnerabilities to the security contact: security@corp.example.com") {
				t.Fatalf("expected security contact instructions, got:\n%s", content)
			}
		})
	}
}

// TestAdoptSecurityPolicy_ExistingFileKeptOnUndeclaredForge verifies that an existing
// SECURITY.md is preserved and verified present when the forge is undeclared.
func TestAdoptSecurityPolicy_ExistingFileKeptOnUndeclaredForge(t *testing.T) {
	repo := newTestRepo(t, "existing-undeclared")
	writeOriginRemote(t, repo, "https://git.internal.example/acme/existing-undeclared.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	customPolicy := "# Security Policy\nCustom reporting instructions.\n"
	mustWrite(t, filepath.Join(repo, securityFile), customPolicy)
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err != nil {
		t.Fatal(err)
	}
	content := mustRead(t, filepath.Join(repo, securityFile))
	if content != customPolicy {
		t.Fatalf("expected existing custom security policy to be preserved, got:\n%s", content)
	}
	if !reportNames(report, securityFile, "Existing security policy verified present") {
		t.Fatalf("expected report to record existing policy preserved: %+v", report.ActionDetails)
	}
}

// TestAdoptSecurityPolicy_InvalidDeclaredForge_Error verifies that an invalid declared
// forge returns an error naming repository.forge.
func TestAdoptSecurityPolicy_InvalidDeclaredForge_Error(t *testing.T) {
	repo := newTestRepo(t, "invalid-forge")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  forge: invalidforge\n")
	_, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err == nil || !strings.Contains(err.Error(), "repository.forge") {
		t.Fatalf("expected error naming repository.forge, got: %v", err)
	}
}

// TestAdoptSecurityPolicy_ExistingFileCancelled_Error verifies that a cancelled context
// during policy observation on an undeclared forge returns an error rather than skipping.
func TestAdoptSecurityPolicy_ExistingFileCancelled_Error(t *testing.T) {
	repo := newTestRepo(t, "cancelled-undeclared")
	writeOriginRemote(t, repo, "https://git.internal.example/acme/cancelled-undeclared.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, securityFile), "# Security Policy\n")
	s := &adoptSession{repoPath: repo, report: &AdoptReport{}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := reconcileSecurityPolicy(ctx, s)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
}
