// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// advisoryLink is the report form of the acme/widgets fixture repository.
const advisoryLink = "https://github.com/acme/widgets/security/advisories/new"

// confidentialLink is a GitLab confidential new-issue form of the fixture repository.
const confidentialLink = "https://gitlab.com/acme/widgets/-/issues/new?issue[confidential]=true"

// securityPolicyTestCase defines one offline policy audit scenario: the policy file, the
// manifest's forge and security section, and the expected output or failure.
type securityPolicyTestCase struct {
	name, path, content, want string
	forge                     config.Forge
	security                  *config.SecurityPolicy
	fail                      bool
}

// declared returns a security section that declares channel, with contact when given.
func declared(channel config.SecurityChannel, contact string) *config.SecurityPolicy {
	return &config.SecurityPolicy{Channel: channel, Contact: contact}
}

// declaredChannelCases are policies checked against a declared security.channel: the artifact
// decides, never the prose around it.
func declaredChannelCases() []securityPolicyTestCase {
	private := declared(config.SecurityChannelPrivateReporting, "")
	confidential := declared(config.SecurityChannelConfidentialIssues, "")
	contact := declared(config.SecurityChannelContact, "security@example.invalid")
	return []securityPolicyTestCase{
		{"missing", "", "", "SECURITY.md missing", config.ForgeGitHub, private, true},
		{"planted refusal of private reporting", "SECURITY.md", "Do not use private vulnerability reporting. Email security@example.invalid.\n",
			"SECURITY.md lacks a link to " + advisoryLink + ", the artifact of security.channel private-reporting", config.ForgeGitHub, private, true},
		{"prose without link", "SECURITY.md", "Use GitHub private vulnerability reporting.\n", "lacks a link to " + advisoryLink, config.ForgeGitHub, private, true},
		{"empty", "SECURITY.md", "", "lacks a link to " + advisoryLink, config.ForgeGitHub, private, true},
		{"markdown link", "SECURITY.md", "Use the [report form](" + advisoryLink + ").\n", "setting not read: --offline", config.ForgeGitHub, private, false},
		{"angle-bracket link", ".github/SECURITY.md", "Report: <" + advisoryLink + ">\n", ".github/SECURITY.md names the private-reporting channel", config.ForgeGitHub, private, false},
		{"bare link at end", "docs/SECURITY.md", "Report at " + advisoryLink, "docs/SECURITY.md names the private-reporting channel", config.ForgeGitHub, private, false},
		{"owner case differs", "SECURITY.md", "[form](https://github.com/ACME/Widgets/security/advisories/new)\n", "setting not read: --offline", config.ForgeGitHub, private, false},
		{"other repository link", "SECURITY.md", "[form](https://github.com/acme/gadgets/security/advisories/new)\n", "lacks a link to " + advisoryLink, config.ForgeGitHub, private, true},
		{"advisories list, not the form", "SECURITY.md", "[advisories](https://github.com/acme/widgets/security/advisories)\n", "lacks a link to " + advisoryLink, config.ForgeGitHub, private, true},
		{"form path prefix only", "SECURITY.md", "https://github.com/acme/widgets/security/advisories/newer\n", "lacks a link to " + advisoryLink, config.ForgeGitHub, private, true},
		{"confidential form", "SECURITY.md", "[form](" + confidentialLink + ")\n", "names the confidential-issues channel", config.ForgeGitLab, confidential, false},
		{"confidential form encoded", "SECURITY.md", "https://gitlab.example.org/g/sub/widgets/-/issues/new?issue%5Bconfidential%5D=true\n", "names the confidential-issues channel", config.ForgeGitLab, confidential, false},
		{"confidential prose only", "SECURITY.md", "Open a confidential issue in this repository.\n", "lacks a link to the new-issue form with issue[confidential]=true", config.ForgeGitLab, confidential, true},
		{"confidential false", "SECURITY.md", "https://gitlab.com/acme/widgets/-/issues/new?issue[confidential]=false\n", "lacks a link to the new-issue form", config.ForgeGitLab, confidential, true},
		{"contact named", "SECURITY.md", "Write to security@example.invalid.\n", "names the contact channel: the security.contact address security@example.invalid", config.ForgeForgejo, contact, false},
		{"contact missing", "SECURITY.md", "Write to other@example.invalid.\n", "lacks the security.contact address security@example.invalid", config.ForgeForgejo, contact, true},
		{"mailto contact bare address", "SECURITY.md", "Write to security@example.invalid.\n", "names the contact channel", config.ForgeGitHub, declared(config.SecurityChannelContact, "mailto:security@example.invalid"), false},
		{"https contact", "SECURITY.md", "Report at <https://example.invalid/security>.\n", "names the contact channel", config.ForgeGitLab, declared(config.SecurityChannelContact, "https://example.invalid/security"), false},
	}
}

// migrationDefaultCases are policies of repositories that declare no security.channel: the
// migration default applies and the audit names it.
func migrationDefaultCases() []securityPolicyTestCase {
	return []securityPolicyTestCase{
		{"default github", "SECURITY.md", "[form](" + advisoryLink + ")\n", "migration default: forge github", config.ForgeGitHub, nil, false},
		{"default github without link", "SECURITY.md", "Use GitHub private vulnerability reporting.\n", "lacks a link to " + advisoryLink, config.ForgeGitHub, nil, true},
		{"default gitlab", "SECURITY.md", "[form](" + confidentialLink + ")\n", "checked as confidential-issues (migration default: forge gitlab)", config.ForgeGitLab, nil, false},
		{"default contact", "SECURITY.md", "Write to security@example.invalid.\n", "checked as contact (migration default: security.contact is set)", config.ForgeForgejo,
			&config.SecurityPolicy{Contact: "security@example.invalid"}, false},
		{"default contact beats forge", "SECURITY.md", "Write to security@example.invalid.\n", "migration default: security.contact is set", config.ForgeGitHub,
			&config.SecurityPolicy{Contact: "security@example.invalid"}, false},
		{"no default on forgejo", "SECURITY.md", "Write to security@example.invalid.\n", "security.channel required: forge forgejo has no native private reporting channel", config.ForgeForgejo, nil, true},
		{"no default on unresolved forge", "SECURITY.md", "[form](" + advisoryLink + ")\n", "security.channel required: an unresolved forge", "", nil, true},
	}
}

// TestAuditCommunityHealthSecurityPolicyOffline plants missing policies and policies without
// their channel's artifact in the existing audit path; offline skips only the setting read,
// never the local requirement.
func TestAuditCommunityHealthSecurityPolicyOffline(t *testing.T) {
	cases := append(declaredChannelCases(), migrationDefaultCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.path != "" {
				writeFixtureFile(t, root, tc.path, tc.content)
			}
			manifest := &config.Manifest{
				Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets", Forge: tc.forge},
				Security:   tc.security,
			}
			out, err := captureStdout(t, func() error {
				return auditLiveForge(t.Context(), manifest, root, &config.ResolvedPolicy{}, true)
			})
			if (err != nil) != tc.fail {
				t.Fatalf("audit error = %v, want failure %v; output: %s", err, tc.fail, out)
			}
			if err != nil {
				out += err.Error()
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("audit output lacks %q: %s", tc.want, out)
			}
		})
	}
}

// TestAuditCommunityHealthSecurityPolicyUndeclaredIdentity accepts any report-form link when
// the manifest names no repository.owner and repository.name to compare it with.
func TestAuditCommunityHealthSecurityPolicyUndeclaredIdentity(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "SECURITY.md", "[form](https://github.com/someone/else/security/advisories/new)\n")
	manifest := &config.Manifest{Repository: config.RepositoryMetadata{Forge: config.ForgeGitHub}}
	out, err := captureStdout(t, func() error {
		return auditSecurityPolicy(t.Context(), manifest, root, actionsForge{notMade: "--offline"})
	})
	if err != nil || !strings.Contains(out, "names the private-reporting channel") {
		t.Fatalf("report-form link without declared identity = %v, %s", err, out)
	}
}

// TestAuditCommunityHealthSecurityPolicyResolvedForge resolves an undeclared forge from the
// origin remote: github.com takes the private-reporting default, and a declared forge-native
// channel the resolved forge does not run fails.
func TestAuditCommunityHealthSecurityPolicyResolvedForge(t *testing.T) {
	f := newAuditFixture(t)
	f.addGitHubOrigin(t)
	writeFixtureFile(t, f.dir, "SECURITY.md", "[form]("+advisoryLink+")\n")
	manifest := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"}}
	out, err := captureStdout(t, func() error {
		return auditSecurityPolicy(t.Context(), manifest, f.dir, actionsForge{notMade: "--offline"})
	})
	if err != nil || !strings.Contains(out, "migration default: forge github") {
		t.Fatalf("origin-resolved github default = %v, %s", err, out)
	}
	manifest.Security = declared(config.SecurityChannelConfidentialIssues, "")
	err = auditSecurityPolicy(t.Context(), manifest, f.dir, actionsForge{notMade: "--offline"})
	if err == nil || !strings.Contains(err.Error(), "security.channel confidential-issues runs on forge gitlab, but the repository's forge is github") {
		t.Fatalf("confidential-issues on a github.com origin = %v", err)
	}
}

// securityReportingStub supplies explicit setting results without a token or forge request.
type securityReportingStub struct {
	enabled bool
	err     error
}

// PrivateVulnerabilityReporting implements the narrow audit reader with an explicit result.
func (s securityReportingStub) PrivateVulnerabilityReporting(context.Context) (bool, error) {
	return s.enabled, s.err
}

// TestAuditCommunityHealthSecuritySetting covers the enabled, mismatched and unread states;
// a failed read must never be interpreted as a disabled setting or a passing comparison.
func TestAuditCommunityHealthSecuritySetting(t *testing.T) {
	cases := []struct {
		name string
		live actionsForge
		want string
		fail bool
	}{
		{"enabled", actionsForge{reporting: securityReportingStub{enabled: true}}, "setting enabled=true", false},
		{"disabled", actionsForge{reporting: securityReportingStub{}}, "enabled=false (mismatch)", true},
		{"network unavailable", actionsForge{reporting: securityReportingStub{err: errors.New("network unavailable")}}, "setting not read: network unavailable", false},
		{"no token", actionsForge{notMade: "no forge token"}, "setting not read: no forge token", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "SECURITY.md", "[form]("+advisoryLink+")\n")
			out, err := captureStdout(t, func() error {
				return auditSecurityPolicy(t.Context(), githubManifest(), root, tc.live)
			})
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v, want failure %v", err, tc.fail)
			}
			if err != nil {
				out += err.Error()
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output lacks %q: %s", tc.want, out)
			}
			if !tc.fail && strings.Contains(out, "not read") && strings.Contains(out, "enabled=true") {
				t.Fatalf("unread setting was reported as enabled: %s", out)
			}
		})
	}
}

// TestAuditCommunityHealthSecuritySettingWiring exercises the existing audit entry point
// and forge driver against a loopback stub; offline must leave its request count unchanged.
func TestAuditCommunityHealthSecuritySettingWiring(t *testing.T) {
	f := newAuditFixture(t)
	f.addGitHubOrigin(t)
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/private-vulnerability-reporting" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reads.Add(1)
		if _, err := w.Write([]byte(`{"enabled":false}`)); err != nil {
			t.Errorf("stub response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	pointLiveChecksAt(t, server.URL)
	manifest := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"}}
	err := auditLiveForge(t.Context(), manifest, f.dir, &config.ResolvedPolicy{}, false)
	if err == nil || !strings.Contains(err.Error(), "SECURITY.md names private vulnerability reporting") || reads.Load() != 1 {
		t.Fatalf("disabled setting = %v; reads %d", err, reads.Load())
	}
	out, err := captureStdout(t, func() error {
		return auditLiveForge(t.Context(), manifest, f.dir, &config.ResolvedPolicy{}, true)
	})
	if err != nil || !strings.Contains(out, "setting not read: --offline") || reads.Load() != 1 {
		t.Fatalf("offline setting = %v, %s; reads %d", err, out, reads.Load())
	}
}

// TestAuditCommunityHealthSecurityPolicyPrecedence checks GitHub's first policy wins:
// an unusable .github policy cannot be excused by a valid policy in another location.
func TestAuditCommunityHealthSecurityPolicyPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "SECURITY.md", "[form]("+advisoryLink+")\n")
	writeFixtureFile(t, root, ".github/SECURITY.md", "# No channel\n")
	err := auditSecurityPolicy(t.Context(), githubManifest(), root, actionsForge{notMade: "--offline"})
	if err == nil || !strings.Contains(err.Error(), ".github/SECURITY.md lacks a link to "+advisoryLink) {
		t.Fatalf("first policy must fail even with valid root policy: %v", err)
	}
}

// TestAuditCommunityHealthSecurityPolicyReadFailure refuses directories and symlinks rather
// than treating a policy that cannot be read as absent or falling back to another file.
func TestAuditCommunityHealthSecurityPolicyReadFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "SECURITY.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := auditSecurityPolicy(t.Context(), &config.Manifest{}, root, actionsForge{}); err == nil ||
		!strings.Contains(err.Error(), "read SECURITY.md") {
		t.Fatalf("directory policy must fail: %v", err)
	}
	root = t.TempDir()
	path := writeFixtureFile(t, t.TempDir(), "policy.md", "[form]("+advisoryLink+")\n")
	if err := os.Symlink(path, filepath.Join(root, "SECURITY.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := auditSecurityPolicy(t.Context(), &config.Manifest{}, root, actionsForge{}); err == nil ||
		!strings.Contains(err.Error(), "read SECURITY.md") {
		t.Fatalf("symlink policy must fail: %v", err)
	}
}

// TestAuditCommunityHealthSecurityPolicyDeclined preserves implicit organisation-health
// declines and explicit adoption declines, while invalid declines still fail closed.
func TestAuditCommunityHealthSecurityPolicyDeclined(t *testing.T) {
	manifest := &config.Manifest{Repository: config.RepositoryMetadata{Name: ".github"}}
	out, err := captureStdout(t, func() error {
		return auditSecurityPolicy(t.Context(), manifest, t.TempDir(), actionsForge{})
	})
	if err != nil || !strings.Contains(out, "SECURITY.md declined") {
		t.Fatalf("organisation-health decline = %v, %s", err, out)
	}
	manifest.Repository.Name = "widgets"
	manifest.Adoption = &config.AdoptionPolicy{Decline: []string{"security-policy"}}
	if err := auditSecurityPolicy(t.Context(), manifest, t.TempDir(), actionsForge{}); err != nil {
		t.Fatalf("explicit decline: %v", err)
	}
	manifest.Adoption.Decline = []string{"unknown-step"}
	if err := auditSecurityPolicy(t.Context(), manifest, t.TempDir(), actionsForge{}); err == nil {
		t.Fatal("invalid decline bypassed policy check")
	}
}

// TestAuditCommunityHealthSecurityPolicyCancelled proves cancellation cannot bypass the
// local file requirement by turning an interrupted read into a successful comparison.
func TestAuditCommunityHealthSecurityPolicyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := auditSecurityPolicy(ctx, &config.Manifest{}, t.TempDir(), actionsForge{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled policy read = %v", err)
	}
}

// githubManifest is the acme/widgets fixture manifest on a declared GitHub forge.
func githubManifest() *config.Manifest {
	return &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets", Forge: config.ForgeGitHub}}
}
