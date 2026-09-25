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
	out, err := adopt.AuditBranchProtectionWithPolicy(manifest, f.dir, policy)
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
