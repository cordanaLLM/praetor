package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAuditBranchProtection_Positive(t *testing.T) {
	// Case 1: Required ruleset is present and no decline
	t.Run("ruleset present and verified", func(t *testing.T) {
		root := t.TempDir()
		rulesetDir := filepath.Join(root, ".github", "rulesets")
		if err := os.MkdirAll(rulesetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rulesetDir, "main.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		manifest := &config.Manifest{}
		summary, err := AuditBranchProtection(manifest, root)
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
		summary, err := AuditBranchProtection(manifest, root)
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
		summary, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(nil, root)
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
		_, err := AuditBranchProtection(manifest, "")
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
		_, err := AuditBranchProtectionWithPolicy(manifest, t.TempDir(), nil)
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
		summary, err := AuditBranchProtectionWithPolicy(manifest, root, policy)
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
		summary, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
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
		_, err := AuditBranchProtection(manifest, root)
		if err == nil {
			t.Fatal("expected failure for missing ruleset with nil adoption policy")
		}
		if !strings.Contains(err.Error(), "[FAIL] Branch protection ruleset .github/rulesets/main.json is missing") {
			t.Fatalf("unexpected error diagnostic: %v", err)
		}
	})
}
