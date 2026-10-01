package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// TestServerAuditBranchRuleset_TDD_Reproduction verifies issue #408 for MCP:
// standards_audit must honor an accepted branch-ruleset adoption decline
// when .github/rulesets/main.json is absent.
func TestServerAuditBranchRuleset_TDD_Reproduction(t *testing.T) {
	srv, root := newFixtureServer(t)

	declinedManifest := `version: 1
repository:
  owner: acme
  name: app
profiles:
  - framework
adoption:
  decline:
    - branch-ruleset
`
	writeFixtureFile(t, root, ".standards.yaml", declinedManifest)

	// Remove ruleset
	rulesetPath := filepath.Join(root, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, srv, "standards_audit", nil)
	if result.IsError {
		t.Fatalf("contradiction reproduced: MCP audit failed despite valid branch-ruleset decline: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "[PASS] Branch protection ruleset declined by adoption.decline.") {
		t.Fatalf("output lacks decline pass notice: %s", result.Content[0].Text)
	}
}

func TestServerAuditBranchRuleset_MCP_Positive_RulesetPresentVerified(t *testing.T) {
	srv, _ := newFixtureServer(t)
	result := callTool(t, srv, "standards_audit", nil)
	expectText(t, "ruleset present", result, "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
}

func TestServerAuditBranchRuleset_MCP_Negative_MissingRulesetFailsClosed(t *testing.T) {
	srv, root := newFixtureServer(t)
	rulesetPath := filepath.Join(root, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, srv, "standards_audit", nil)
	expectError(t, "missing ruleset", result, "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing")
}

// TestServerAuditBranchRuleset_MCP_Negative_PlaceholderRulesetFails pins BUG-267 for MCP: the
// audit reported "{}" verified because it checked only that the file existed.
func TestServerAuditBranchRuleset_MCP_Negative_PlaceholderRulesetFails(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".github/rulesets/main.json", "{}\n")
	result := callTool(t, srv, "standards_audit", nil)
	expectError(t, "placeholder ruleset", result, "does not match the declared policy")
	if strings.Contains(result.Content[0].Text, "[PASS] Branch protection") {
		t.Fatalf("placeholder ruleset produced pass evidence: %s", result.Content[0].Text)
	}
}

func TestServerAuditBranchRuleset_MCP_Negative_MalformedDeclineFailsClosed(t *testing.T) {
	srv, root := newFixtureServer(t)
	malformedManifest := `version: 1
repository:
  owner: fixture
  name: repo
profiles:
  - framework
adoption:
  decline:
    - invalid-unknown-artefact
`
	writeFixtureFile(t, root, ".standards.yaml", malformedManifest)

	// The first gate that reads adoption.decline fails closed; since #600 that is the context
	// gate, which reads the agent-harness decline, ahead of the branch protection gate.
	result := callTool(t, srv, "standards_audit", nil)
	expectError(t, "unknown decline", result, "audit failed: adoption cannot decline unknown artefact")
	if strings.Contains(result.Content[0].Text, "[PASS] Branch protection") {
		t.Fatalf("malformed decline produced pass evidence: %s", result.Content[0].Text)
	}
}

func TestServerAuditBranchRuleset_MCP_Boundary_PolicyNotRequired(t *testing.T) {
	_, root := newFixtureServer(t)
	rulesetPath := filepath.Join(root, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	manifest := &config.Manifest{
		Repository: config.RepositoryMetadata{Owner: "fixture", Name: "repo"},
	}
	policy := &config.ResolvedPolicy{
		BranchProtection: config.BranchProtectionPolicy{
			EnforceLinearHistory: false,
			RequireSignedCommits: false,
		},
	}
	out, err := adopt.AuditBranchProtectionWithPolicy(t.Context(), manifest, root, policy)
	if err != nil {
		t.Fatalf("audit failed when policy does not require ruleset: %v\nOutput: %s", err, out)
	}
	if out != "[PASS] Branch protection ruleset not required by policy." {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestServerAuditBranchRuleset_MCP_Boundary_MissingManifestFailsClosed(t *testing.T) {
	srv, root := newFixtureServer(t)
	manifestPath := filepath.Join(root, ".standards.yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, srv, "standards_audit", nil)
	expectError(t, "missing manifest", result, "Effective policy audit failed")
	if strings.Contains(result.Content[0].Text, "[PASS] Branch protection") {
		t.Fatalf("missing manifest produced pass evidence: %s", result.Content[0].Text)
	}
}

func TestServerAuditBranchRuleset_MCP_CLI_Parity(t *testing.T) {
	cases := []struct {
		name        string
		manifest    *config.Manifest
		withRuleset bool
		wantPass    string
		wantErr     string
	}{
		{
			name:        "ruleset present verified",
			manifest:    &config.Manifest{},
			withRuleset: true,
			wantPass:    "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.",
		},
		{
			name: "ruleset declined absent",
			manifest: &config.Manifest{
				Adoption: &config.AdoptionPolicy{
					Decline: []string{"branch-ruleset"},
				},
			},
			withRuleset: false,
			wantPass:    "[PASS] Branch protection ruleset declined by adoption.decline.",
		},
		{
			name:        "ruleset missing fails closed",
			manifest:    &config.Manifest{},
			withRuleset: false,
			wantErr:     "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing while policy requires linear history or signed commits; run 'praetorctl sync' to reconcile",
		},
		{
			name: "unknown decline fails closed",
			manifest: &config.Manifest{
				Adoption: &config.AdoptionPolicy{
					Decline: []string{"bogus-step"},
				},
			},
			withRuleset: false,
			wantErr:     "adoption cannot decline unknown artefact",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.withRuleset {
				dir := filepath.Join(root, ".github", "rulesets")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "main.json"), []byte(fixtureRuleset(t)), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// CLI path (adopt.AuditBranchProtectionWithPolicy directly)
			policy := config.DefaultPolicy()
			cliOut, cliErr := adopt.AuditBranchProtectionWithPolicy(t.Context(), tc.manifest, root, policy)
			// MCP path (auditBranchProtection delegates to adopt.AuditBranchProtectionWithPolicy)
			mcpOut, mcpErr := auditBranchProtection(t.Context(), tc.manifest, root, policy)

			if tc.wantErr != "" {
				if cliErr == nil || !strings.Contains(cliErr.Error(), tc.wantErr) {
					t.Fatalf("CLI expected error containing %q, got %v", tc.wantErr, cliErr)
				}
				if mcpErr == nil || !strings.Contains(mcpErr.Error(), tc.wantErr) {
					t.Fatalf("MCP expected error containing %q, got %v", tc.wantErr, mcpErr)
				}
				if cliErr.Error() != mcpErr.Error() {
					t.Fatalf("parity mismatch on error: CLI %q vs MCP %q", cliErr.Error(), mcpErr.Error())
				}
			} else {
				if cliErr != nil {
					t.Fatalf("CLI unexpected error: %v", cliErr)
				}
				if mcpErr != nil {
					t.Fatalf("MCP unexpected error: %v", mcpErr)
				}
				if cliOut != tc.wantPass {
					t.Fatalf("CLI got %q, want %q", cliOut, tc.wantPass)
				}
				if mcpOut != tc.wantPass {
					t.Fatalf("MCP got %q, want %q", mcpOut, tc.wantPass)
				}
				if cliOut != mcpOut {
					t.Fatalf("parity mismatch on output: CLI %q vs MCP %q", cliOut, mcpOut)
				}
			}
		})
	}
}

// signingFrameworkProfile is a framework archetype that contributes branch protection, as
// every shipped archetype does: it requires signed commits, which the built-in defaults do not.
const signingFrameworkProfile = "id: framework\nname: Framework\nbranch_protection:\n  require_signed_commits: true\n"

// useSigningProfile swaps the fixture's framework archetype for signingFrameworkProfile,
// re-pins the lockfile to it and returns the branch protection the effective policy resolves.
// The fixture manifest carries no override, so signed commits come from the profile alone.
func useSigningProfile(t *testing.T, root string) config.BranchProtectionPolicy {
	t.Helper()
	writeFixtureFile(t, root, ".config/archetypes/framework.yaml", signingFrameworkProfile)
	writeFixtureFile(t, root, ".standards.lock", auditLockFor(t, signingFrameworkProfile))
	effective, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{
		Root: root, ManifestPath: filepath.Join(root, ".standards.yaml"), Audit: true,
	})
	if err != nil {
		t.Fatalf("resolve fixture effective policy: %v", err)
	}
	if !effective.Policy.BranchProtection.RequireSignedCommits {
		t.Fatal("fixture precondition: the profile must contribute require_signed_commits")
	}
	return effective.Policy.BranchProtection
}

// TestServerAuditBranchRuleset_MCP_Positive_ProfileContributesBranchProtection pins the
// effective policy for standards_audit: it compared against built-in defaults plus overrides,
// so an adopter whose profile requires signed commits failed on the ruleset adopt wrote.
func TestServerAuditBranchRuleset_MCP_Positive_ProfileContributesBranchProtection(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".github/rulesets/main.json", renderFixtureRuleset(t, useSigningProfile(t, root)))
	result := callTool(t, srv, "standards_audit", nil)
	expectText(t, "profile ruleset", result, "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
}

// TestServerAuditBranchRuleset_MCP_Negative_DefaultsRulesetUnderSigningProfile is the other
// direction: a defaults-rendered ruleset omits the signature rule the profile demands.
func TestServerAuditBranchRuleset_MCP_Negative_DefaultsRulesetUnderSigningProfile(t *testing.T) {
	srv, root := newFixtureServer(t)
	useSigningProfile(t, root)
	writeFixtureFile(t, root, ".github/rulesets/main.json", renderFixtureRuleset(t, config.DefaultPolicy().BranchProtection))
	result := callTool(t, srv, "standards_audit", nil)
	expectError(t, "defaults ruleset", result, "does not match the declared policy")
}
