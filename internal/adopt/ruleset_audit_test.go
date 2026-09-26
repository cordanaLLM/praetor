package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestAuditBranchProtection_Positive(t *testing.T) {
	// Case 1: Required ruleset is the one the declared policy renders, and no decline
	t.Run("ruleset present and verified", func(t *testing.T) {
		root := t.TempDir()
		writeAuditRuleset(t, root, renderAuditRuleset(t, config.DefaultPolicy().BranchProtection, nil))

		manifest := &config.Manifest{}
		summary, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified." {
			t.Fatalf("unexpected summary: %q", summary)
		}
	})

	// Case 2: Branch ruleset explicitly declined, ruleset absent
	t.Run("ruleset declined and absent", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{"branch-ruleset"},
			},
		}
		summary, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "[PASS] Branch protection ruleset declined by adoption.decline." {
			t.Fatalf("unexpected summary: %q", summary)
		}
	})

	// Case 3: Branch ruleset explicitly declined, ruleset present
	t.Run("ruleset declined and present", func(t *testing.T) {
		root := t.TempDir()
		rulesetDir := filepath.Join(root, ".github", "rulesets")
		if err := os.MkdirAll(rulesetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rulesetDir, "main.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{"branch-ruleset"},
			},
		}
		summary, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "[PASS] Branch protection ruleset declined by adoption.decline." {
			t.Fatalf("unexpected summary: %q", summary)
		}
	})
}

func TestAuditBranchProtection_Negative(t *testing.T) {
	// Case 1: Required ruleset is absent and not declined
	t.Run("ruleset required and missing fails closed", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for missing ruleset without decline")
		}
		if !strings.Contains(err.Error(), "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 2: Unknown decline in adoption.decline fails closed
	t.Run("unknown decline fails closed", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{"unknown-artefact-name"},
			},
		}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for unknown decline")
		}
		if !strings.Contains(err.Error(), "adoption cannot decline unknown artefact") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 3: Mandatory decline rejected
	t.Run("mandatory decline rejected", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{"manifest"},
			},
		}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for mandatory decline")
		}
		if !strings.Contains(err.Error(), "adoption cannot decline \"manifest\"") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 4: Oversized decline list fails closed
	t.Run("oversized declines list fails closed", func(t *testing.T) {
		root := t.TempDir()
		declines := make([]string, maxDeclinedArtifacts+1)
		for i := range declines {
			declines[i] = "branch-ruleset"
		}
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: declines,
			},
		}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for oversized decline list")
		}
		if !strings.Contains(err.Error(), "adoption declines at most") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 5: Nil manifest fails closed
	t.Run("nil manifest fails closed", func(t *testing.T) {
		root := t.TempDir()
		_, err := AuditBranchProtectionWithPolicy(t.Context(), nil, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for nil manifest")
		}
		if !strings.Contains(err.Error(), "manifest is required") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 6: Empty root dir fails closed
	t.Run("empty root fails closed", func(t *testing.T) {
		manifest := &config.Manifest{}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, "", config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for empty root")
		}
		if !strings.Contains(err.Error(), "repository root is required") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 7: Nil policy fails closed
	t.Run("nil policy fails closed", func(t *testing.T) {
		manifest := &config.Manifest{}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, t.TempDir(), nil)
		if err == nil {
			t.Fatal("expected failure for nil policy")
		}
		if !strings.Contains(err.Error(), "policy is required") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})
}

func TestAuditBranchProtection_Boundary(t *testing.T) {
	// Case 1: Policy does not require ruleset -> passes even if missing and undeclined
	t.Run("policy does not require ruleset", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{}
		policy := &config.ResolvedPolicy{
			BranchProtection: config.BranchProtectionPolicy{
				EnforceLinearHistory: false,
				RequireSignedCommits: false,
			},
		}
		summary, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "[PASS] Branch protection ruleset not required by policy." {
			t.Fatalf("unexpected summary: %q", summary)
		}
	})

	// Case 2: Case and whitespace normalization in decline list
	t.Run("case and whitespace normalization", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{"  BRANCH-RULESET  "},
			},
		}
		summary, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "[PASS] Branch protection ruleset declined by adoption.decline." {
			t.Fatalf("unexpected summary: %q", summary)
		}
	})

	// Case 3: Empty decline list with missing ruleset fails closed
	t.Run("empty decline list with missing ruleset fails closed", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: &config.AdoptionPolicy{
				Decline: []string{},
			},
		}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for missing ruleset with empty decline list")
		}
		if !strings.Contains(err.Error(), "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})

	// Case 4: Nil adoption policy in manifest with missing ruleset fails closed
	t.Run("nil adoption policy with missing ruleset fails closed", func(t *testing.T) {
		root := t.TempDir()
		manifest := &config.Manifest{
			Adoption: nil,
		}
		_, err := AuditBranchProtectionWithPolicy(t.Context(), manifest, root, config.DefaultPolicy())
		if err == nil {
			t.Fatal("expected failure for missing ruleset with nil adoption policy")
		}
		if !strings.Contains(err.Error(), "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})
}

// signedPolicy is a policy that requires every protection the ruleset carries: linear
// history, signed commits and one independent approval.
func signedPolicy() config.BranchProtectionPolicy {
	return config.BranchProtectionPolicy{
		EnforceLinearHistory:       true,
		RequireSignedCommits:       true,
		RequiredApprovingReviewers: 1,
		DismissStaleReviews:        true,
		ReviewMode:                 config.BranchReviewModeIndependent,
	}
}

func renderAuditRuleset(t *testing.T, policy config.BranchProtectionPolicy, contexts []string) string {
	t.Helper()
	data, err := forge.RenderRepositoryRuleset(policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeAuditRuleset(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".github", "rulesets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAuditBranchProtection_Negative_ContentMustEnforcePolicy pins BUG-041 and BUG-267: the
// audit checked only that the ruleset existed, so each of these was reported verified.
func TestAuditBranchProtection_Negative_ContentMustEnforcePolicy(t *testing.T) {
	policy := &config.ResolvedPolicy{BranchProtection: signedPolicy()}
	weaker := signedPolicy()
	weaker.RequiredApprovingReviewers = 0
	unsigned := signedPolicy()
	unsigned.RequireSignedCommits = false
	cases := map[string]string{
		"empty object":           "{}\n",
		"zero approvals":         renderAuditRuleset(t, weaker, nil),
		"no required_signatures": renderAuditRuleset(t, unsigned, nil),
		"not JSON":               "rules: []\n",
		"duplicate keys":         `{"rules": [], "rules": []}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeAuditRuleset(t, root, content)
			summary, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, policy)
			if err == nil || !strings.Contains(err.Error(), "[FAIL] Branch protection ruleset .github/rulesets/main.json does not match the declared policy") {
				t.Fatalf("a ruleset that does not enforce the policy must fail, got %q, %v", summary, err)
			}
		})
	}
}

// TestAuditBranchProtection_Negative_MissingWorkflowCheck asserts the ruleset must require the
// status checks the repository's workflows report, as sync requires.
func TestAuditBranchProtection_Negative_MissingWorkflowCheck(t *testing.T) {
	root := t.TempDir()
	writeAuditWorkflow(t, root)
	writeAuditRuleset(t, root, renderAuditRuleset(t, signedPolicy(), nil))
	policy := &config.ResolvedPolicy{BranchProtection: signedPolicy()}
	if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, policy); err == nil {
		t.Fatal("a ruleset that omits a workflow's required check must fail")
	}
}

// TestAuditBranchProtection_Boundary_FormattingIsNotContent asserts a ruleset that differs from
// the rendered one only in key order and whitespace passes, and that a ruleset carrying the
// workflow-derived checks passes.
func TestAuditBranchProtection_Boundary_FormattingIsNotContent(t *testing.T) {
	policy := &config.ResolvedPolicy{BranchProtection: signedPolicy()}
	t.Run("key order and whitespace", func(t *testing.T) {
		root := t.TempDir()
		var doc map[string]any
		if err := json.Unmarshal([]byte(renderAuditRuleset(t, signedPolicy(), nil)), &doc); err != nil {
			t.Fatal(err)
		}
		compact, err := json.Marshal(doc) // sorted keys, no indentation
		if err != nil {
			t.Fatal(err)
		}
		writeAuditRuleset(t, root, string(compact))
		if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, policy); err != nil {
			t.Fatalf("formatting differences must not fail the audit: %v", err)
		}
	})
	t.Run("workflow checks present", func(t *testing.T) {
		root := t.TempDir()
		writeAuditWorkflow(t, root)
		contexts, err := forge.RequiredStatusContexts(t.Context(), root)
		if err != nil || len(contexts) == 0 {
			t.Fatalf("fixture workflow must report a status context: %v, %v", contexts, err)
		}
		writeAuditRuleset(t, root, renderAuditRuleset(t, signedPolicy(), contexts))
		if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, policy); err != nil {
			t.Fatalf("the rendered ruleset with workflow checks must pass: %v", err)
		}
	})
}

func writeAuditWorkflow(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const workflow = "name: CI\non:\n  pull_request:\njobs:\n  test:\n    name: Unit Tests\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
}
