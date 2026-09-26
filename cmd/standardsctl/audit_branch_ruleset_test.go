package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// TestAuditBranchRuleset_TDD_Reproduction verifies issue #408:
// standardsctl adopt accepts and records adoption.decline with branch-ruleset,
// omits .github/rulesets/main.json, and audit must honor the decline coherently.
func TestAuditBranchRuleset_TDD_Reproduction(t *testing.T) {
	f := newAuditFixture(t)

	// Manifest declaring adoption.decline with branch-ruleset
	declinedManifest := `version: 1
repository:
  owner: acme
  name: widgets
profiles:
  - framework
facets:
  - security:high
adoption:
  decline:
    - branch-ruleset
    - dev-container
`
	writeFixtureFile(t, f.dir, ".standards.yaml", declinedManifest)
	initGitFixture(t, f.dir)
	writeFixtureFile(t, f.dir, ".git/hooks/pre-commit", "#!/bin/sh\nexit 0\n")

	// Remove ruleset to simulate clean adoption run
	rulesetPath := filepath.Join(f.dir, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	// Run adopt
	report, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{
		Path:    f.dir,
		Profile: "framework",
	})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}

	// Verify adopt skipped and declined branch-ruleset, leaving main.json absent
	if _, err := os.Stat(rulesetPath); !os.IsNotExist(err) {
		t.Fatalf("adopt created .github/rulesets/main.json despite adoption.decline: %v", err)
	}
	skipped := false
	for _, action := range report.ActionDetails {
		if action.Path == "branch-ruleset" && strings.Contains(action.Details, "Declined by adoption.decline") {
			skipped = true
			break
		}
	}
	if !skipped {
		t.Fatalf("adopt report did not record branch-ruleset skip: %+v", report.ActionDetails)
	}

	// Run audit: must pass coherently instead of failing unconditionally
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("contradiction reproduced: adopt succeeded with branch-ruleset decline, but audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Branch protection ruleset declined by adoption.decline.")
}

func TestAuditBranchRuleset_CLI_Positive_RulesetPresentVerified(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
}

func TestAuditBranchRuleset_CLI_Negative_MissingRulesetFailsClosed(t *testing.T) {
	f := newAuditFixture(t)
	rulesetPath := filepath.Join(f.dir, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	_, err := f.audit(t)
	if err == nil {
		t.Fatal("expected audit to fail when ruleset is missing and not declined")
	}
	mustErrContain(t, err, "Branch protection ruleset .github/rulesets/main.json is missing")
}

// TestAuditBranchRuleset_CLI_Negative_PlaceholderRulesetFails pins BUG-267 end to end: audit
// reported "{}" verified because it checked only that the file existed.
func TestAuditBranchRuleset_CLI_Negative_PlaceholderRulesetFails(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".github/rulesets/main.json", "{}\n")
	out, err := f.audit(t)
	if err == nil {
		t.Fatalf("audit must fail on a placeholder ruleset:\n%s", out)
	}
	mustErrContain(t, err, "does not match the declared policy")
	if strings.Contains(out, "[PASS] Branch protection") {
		t.Fatalf("placeholder ruleset produced pass evidence: %s", out)
	}
}

func TestAuditBranchRuleset_CLI_Negative_MalformedDeclineFailsClosed(t *testing.T) {
	f := newAuditFixture(t)
	malformedManifest := `version: 1
repository:
  owner: acme
  name: widgets
profiles:
  - framework
facets:
  - security:high
adoption:
  decline:
    - invalid-unknown-artefact
`
	writeFixtureFile(t, f.dir, ".standards.yaml", malformedManifest)

	out, err := f.audit(t)
	if err == nil {
		t.Fatal("expected audit to fail for unknown decline in manifest")
	}
	mustErrContain(t, err, "adoption cannot decline unknown artefact")
	if strings.Contains(out, "[PASS] Branch protection") {
		t.Fatalf("malformed decline produced pass evidence: %s", out)
	}
}

func TestAuditBranchRuleset_CLI_Boundary_PolicyNotRequired(t *testing.T) {
	f := newAuditFixture(t)
	rulesetPath := filepath.Join(f.dir, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}

	manifest := &config.Manifest{
		Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"},
	}
	policy := &config.ResolvedPolicy{
		BranchProtection: config.BranchProtectionPolicy{
			EnforceLinearHistory: false,
			RequireSignedCommits: false,
		},
	}
	out, err := adopt.AuditBranchProtectionWithPolicy(t.Context(), manifest, f.dir, policy)
	if err != nil {
		t.Fatalf("audit failed when policy does not require ruleset: %v\nOutput: %s", err, out)
	}
	if out != "[PASS] Branch protection ruleset not required by policy." {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestAuditBranchRuleset_CLI_Boundary_MissingManifestFailsClosed(t *testing.T) {
	f := newAuditFixture(t)
	manifestPath := filepath.Join(f.dir, ".standards.yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}

	out, err := f.audit(t)
	if err == nil {
		t.Fatal("expected audit to fail when manifest is missing")
	}
	mustErrContain(t, err, "Manifest audit failed")
	if strings.Contains(out, "[PASS] Branch protection") {
		t.Fatalf("missing manifest produced pass evidence: %s", out)
	}
}

// signingFrameworkProfile is a framework archetype that contributes branch protection, as
// every shipped archetype does: it requires signed commits, which the built-in defaults do not.
const signingFrameworkProfile = "id: \"framework\"\nname: \"Framework\"\nbranch_protection:\n  require_signed_commits: true\n"

// useSigningProfile swaps the fixture's framework archetype for signingFrameworkProfile,
// re-pins the lockfile to it and returns the branch protection the effective policy resolves.
// The manifest carries no override, so signed commits come from the profile alone.
func useSigningProfile(t *testing.T, f *auditFixture) config.BranchProtectionPolicy {
	t.Helper()
	writeFixtureFile(t, f.dir, ".config/archetypes/framework.yaml", signingFrameworkProfile)
	lf := &lockFixture{dir: f.dir}
	lf.writeLock(t,
		lf.digestOf(t, ".config/archetypes/framework.yaml"),
		lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"),
		"")
	effective, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{
		Root: f.dir, ManifestPath: f.manifestPath, Audit: true,
	})
	if err != nil {
		t.Fatalf("resolve fixture effective policy: %v", err)
	}
	if !effective.Policy.BranchProtection.RequireSignedCommits {
		t.Fatal("fixture precondition: the profile must contribute require_signed_commits")
	}
	return effective.Policy.BranchProtection
}

// TestAuditBranchRuleset_CLI_Positive_ProfileContributesBranchProtection pins the effective
// policy: adopt and sync render the ruleset from profiles, facets and overrides joined, so the
// audit must compare against the same policy. It compared against built-in defaults plus
// overrides, so an adopter whose profile requires signed commits failed audit on the very
// ruleset adopt wrote.
func TestAuditBranchRuleset_CLI_Positive_ProfileContributesBranchProtection(t *testing.T) {
	f := newAuditFixture(t)
	writeDeclaredRuleset(t, f.dir, useSigningProfile(t, f))
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("the ruleset rendered from the effective policy must pass: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
}

// TestAuditBranchRuleset_CLI_Negative_DefaultsRulesetUnderSigningProfile is the other direction:
// a ruleset rendered from defaults plus overrides omits required_signatures the profile demands.
func TestAuditBranchRuleset_CLI_Negative_DefaultsRulesetUnderSigningProfile(t *testing.T) {
	f := newAuditFixture(t)
	useSigningProfile(t, f)
	writeDeclaredRuleset(t, f.dir, config.DefaultPolicy().BranchProtection)
	out, err := f.audit(t)
	if err == nil {
		t.Fatalf("a ruleset without the profile's signature rule must fail:\n%s", out)
	}
	mustErrContain(t, err, "does not match the declared policy")
}
