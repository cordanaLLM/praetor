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

// securityPolicyTestCase defines one offline policy audit scenario.
type securityPolicyTestCase struct {
	name, path, content, want string
	forge                     config.Forge
	fail                      bool
}

// offlinePolicyCases returns the table of offline community-health security policy scenarios.
func offlinePolicyCases() []securityPolicyTestCase {
	return []securityPolicyTestCase{
		{"missing", "", "", "SECURITY.md missing", "", true},
		{"no channel", "SECURITY.md", "# Security\nReport bugs publicly.\n", "SECURITY.md names no private reporting channel", "", true},
		{"empty", "SECURITY.md", "", "SECURITY.md names no private reporting channel", "", true},
		{"vague maintainers", "SECURITY.md", "Report privately to the maintainers.\n", "SECURITY.md names no private reporting channel", "", true},
		{"root", "SECURITY.md", "Use GitHub private vulnerability reporting.\n", "setting not read: --offline", "", false},
		{"github", ".github/SECURITY.md", "Use GitHub private vulnerability reporting.\n", ".github/SECURITY.md", "", false},
		{"docs", "docs/SECURITY.md", "Use GitHub private vulnerability reporting.\n", "docs/SECURITY.md", "", false},
		{"security contact", "SECURITY.md", "Security contact: security@example.invalid\n", "SECURITY.md", "", false},
		{"mailto", "SECURITY.md", "Report privately to [security](mailto:security@example.invalid).\n", "SECURITY.md", "", false},
		{"email instruction", "SECURITY.md", "Email to security@example.invalid.\n", "SECURITY.md", "", false},
		{"empty contact", "SECURITY.md", "Security contact:\n", "SECURITY.md names no private reporting channel", "", true},
		{"wrapped phrase", "SECURITY.md", "Use GitHub PRIVATE vulnerability\nreporting.\n", "setting not read: --offline", "", false},
		{"negated phrase", "SECURITY.md", "Do not use private vulnerability reporting.\n", "SECURITY.md names no private reporting channel", "", true},
		{"disabled phrase", "SECURITY.md", "Private vulnerability reporting is disabled.\n", "SECURITY.md names no private reporting channel", "", true},
		{"non-instructional mention", "SECURITY.md", "We considered private vulnerability reporting.\n", "SECURITY.md names no private reporting channel", "", true},
		{"negated with contact", "SECURITY.md", "Do not use private vulnerability reporting. Security contact: security@example.invalid\n", "policy uses a security contact", "", false},
		{"confidential issues", "SECURITY.md", "Open a confidential issue in this repository to report vulnerabilities.\n", "policy uses confidential issues", "", false},
		{"confidential issues gitlab", "SECURITY.md", "Open a confidential issue in this repository to report vulnerabilities.\n", "policy uses confidential issues", config.ForgeGitLab, false},
		{"confidential issues github", "SECURITY.md", "Open a confidential issue in this repository to report vulnerabilities.\n", "names confidential issues, but repository.forge is github", config.ForgeGitHub, true},
		{"private reporting with contact gitlab", "SECURITY.md", "Use GitHub private vulnerability reporting.\nSecurity contact: security@example.invalid\n", "names GitHub private vulnerability reporting, but repository.forge is gitlab", config.ForgeGitLab, true},
		{"do not open public issue", "SECURITY.md", "Do not open a public issue, use GitHub private vulnerability reporting.\n", "setting not read: --offline", "", false},
		{"do not report in public issues", "SECURITY.md", "Please do not report vulnerabilities in public issues. Instead use GitHub private vulnerability reporting.\n", "setting not read: --offline", "", false},
		{"disabled for this repository", "SECURITY.md", "Private vulnerability reporting is disabled for this repository.\n", "SECURITY.md names no private reporting channel", "", true},
		{"we do not use", "SECURITY.md", "We do not use private vulnerability reporting.\n", "SECURITY.md names no private reporting channel", "", true},
		{"do not hesitate", "SECURITY.md", "Please do not hesitate to use GitHub private vulnerability reporting.\n", "setting not read: --offline", "", false},
		{"do not currently use", "SECURITY.md", "We do not currently use private vulnerability reporting.\n", "SECURITY.md names no private reporting channel", "", true},
		{"without delay", "SECURITY.md", "Report without delay via GitHub private vulnerability reporting.\n", "setting not read: --offline", "", false},
		{"not only", "SECURITY.md", "Report not only via GitHub private vulnerability reporting, but also email.\n", "setting not read: --offline", "", false},
		{"https contact without host", "SECURITY.md", "Security contact: https:///report\n", "SECURITY.md names no private reporting channel", "", true},
		{"https contact valid", "SECURITY.md", "Security contact: https://example.invalid/report\n", "SECURITY.md", "", false},
		{"private reporting by email gitlab", "SECURITY.md", "Use private vulnerability reporting by email. Security contact: security@example.com\n", "policy uses a security contact", config.ForgeGitLab, false},
		{"github private reporting gitlab", "SECURITY.md", "Use GitHub private vulnerability reporting\n", "names GitHub private vulnerability reporting, but repository.forge is gitlab", config.ForgeGitLab, true},
		{"bare private reporting undeclared", "SECURITY.md", "Use private vulnerability reporting.\n", "SECURITY.md names no private reporting channel", "", true},
		{"security tab marker", "SECURITY.md", "Use private vulnerability reporting: open this repository's Security tab.\n", "setting not read: --offline", "", false},
		{"advisory link marker", "SECURITY.md", "Report via private vulnerability reporting: https://github.com/acme/widgets/security/advisories/new\n", "setting not read: --offline", "", false},
	}
}

// TestAuditCommunityHealthSecurityPolicyOffline plants missing and unusable policies in
// the existing audit path; offline skips only the setting read, never the local requirement.
func TestAuditCommunityHealthSecurityPolicyOffline(t *testing.T) {
	for _, tc := range offlinePolicyCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.path != "" {
				writeFixtureFile(t, root, tc.path, tc.content)
			}
			manifest := &config.Manifest{Repository: config.RepositoryMetadata{Name: "widgets", Forge: tc.forge}}
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
			writeFixtureFile(t, root, "SECURITY.md", "Use GitHub private vulnerability reporting.\n")
			out, err := captureStdout(t, func() error {
				return auditSecurityPolicy(t.Context(), &config.Manifest{}, root, tc.live)
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
	writeFixtureFile(t, root, "SECURITY.md", "Use GitHub private vulnerability reporting.\n")
	writeFixtureFile(t, root, ".github/SECURITY.md", "# No channel\n")
	err := auditSecurityPolicy(t.Context(), &config.Manifest{}, root, actionsForge{notMade: "--offline"})
	if err == nil || !strings.Contains(err.Error(), ".github/SECURITY.md names no private reporting channel") {
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
	path := writeFixtureFile(t, t.TempDir(), "policy.md", "Use GitHub private vulnerability reporting.\n")
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

// TestAuditCommunityHealthSecurityPolicyForgeMismatch proves that a non-GitHub repository
// whose policy specifies GitHub's private vulnerability reporting channel fails the audit,
// even when an actionable security contact is also present.
func TestAuditCommunityHealthSecurityPolicyForgeMismatch(t *testing.T) {
	cases := []struct {
		name, content string
		forge         config.Forge
	}{
		{"bare forgejo", "Use GitHub private vulnerability reporting.\n", config.ForgeForgejo},
		{"with contact forgejo", "Use GitHub private vulnerability reporting.\nSecurity contact: security@example.invalid\n", config.ForgeForgejo},
		{"with contact gitlab", "Use GitHub private vulnerability reporting.\nSecurity contact: security@example.invalid\n", config.ForgeGitLab},
		{"bare gitlab", "Use GitHub private vulnerability reporting.\n", config.ForgeGitLab},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "SECURITY.md", tc.content)
			manifest := &config.Manifest{
				Repository: config.RepositoryMetadata{
					Name:  "widgets",
					Forge: tc.forge,
				},
			}
			err := auditSecurityPolicy(t.Context(), manifest, root, actionsForge{notMade: "--offline"})
			if err == nil || !strings.Contains(err.Error(), "names GitHub private vulnerability reporting, but repository.forge is "+string(tc.forge)) {
				t.Fatalf("forge mismatch not rejected for %s: %v", tc.name, err)
			}
		})
	}
}
