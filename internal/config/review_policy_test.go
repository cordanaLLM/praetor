package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchProtectionReviewModeSingleMaintainerRetainsConfiguredMinimum(t *testing.T) {
	policy := DefaultPolicy()
	policy.ApplyOverrides(Overrides{BranchProtection: &BranchProtectionPolicy{
		RequiredApprovingReviewers: 4,
		ReviewMode:                 BranchReviewModeSingleMaintainer,
	}})

	count, codeOwner, err := policy.BranchProtection.EffectiveReviewRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || codeOwner {
		t.Fatalf("single-maintainer mode resolved count=%d code_owner=%v", count, codeOwner)
	}
	if policy.BranchProtection.RequiredApprovingReviewers != 4 {
		t.Fatalf("configured reviewer minimum was lost: %+v", policy.BranchProtection)
	}
}

func TestLoadManifestSingleMaintainerReviewMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	body := "version: 1\noverrides:\n  branch_protection:\n    required_approving_reviewers: 4\n    review_mode: single_maintainer\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	count, codeOwner, err := policy.BranchProtection.EffectiveReviewRequirements()
	if err != nil || count != 0 || codeOwner || policy.BranchProtection.RequiredApprovingReviewers != 4 {
		t.Fatalf("manifest opt-in did not retain its configured minimum: count=%d code_owner=%v policy=%+v err=%v", count, codeOwner, policy.BranchProtection, err)
	}
}

func TestBranchProtectionReviewModeDefaultAndRestore(t *testing.T) {
	for _, mode := range []BranchReviewMode{"", BranchReviewModeIndependent} {
		policy := BranchProtectionPolicy{RequiredApprovingReviewers: 3, ReviewMode: mode}
		count, codeOwner, err := policy.EffectiveReviewRequirements()
		if err != nil {
			t.Fatal(err)
		}
		if count != 3 || !codeOwner {
			t.Fatalf("mode %q resolved count=%d code_owner=%v", mode, count, codeOwner)
		}
	}

	restored := DefaultPolicy()
	restored.ApplyOverrides(Overrides{BranchProtection: &BranchProtectionPolicy{RequiredApprovingReviewers: 4}})
	count, codeOwner, err := restored.BranchProtection.EffectiveReviewRequirements()
	if err != nil || count != 4 || !codeOwner {
		t.Fatalf("removing review mode did not restore the configured minimum: count=%d code_owner=%v err=%v", count, codeOwner, err)
	}
}

func TestBranchProtectionReviewModeRejectsInvalidConfiguration(t *testing.T) {
	for _, policy := range []BranchProtectionPolicy{
		{RequiredApprovingReviewers: -1},
		{RequiredApprovingReviewers: 1, ReviewMode: "unreviewed"},
	} {
		if _, _, err := policy.EffectiveReviewRequirements(); err == nil {
			t.Fatalf("invalid review policy accepted: %+v", policy)
		}
	}

	for _, value := range []string{"unreviewed", `""`, "null", "1"} {
		path := filepath.Join(t.TempDir(), ".standards.yaml")
		body := "version: 1\noverrides:\n  branch_protection:\n    required_approving_reviewers: 1\n    review_mode: " + value + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(path); err == nil {
			t.Fatalf("manifest with invalid review mode %s was accepted", value)
		}
	}

	joined := Join(&ResolvedPolicy{BranchProtection: BranchProtectionPolicy{ReviewMode: "unreviewed"}}, DefaultPolicy())
	if _, _, err := joined.BranchProtection.EffectiveReviewRequirements(); err == nil {
		t.Fatal("policy join silently discarded an unknown review mode")
	}
}

func TestEffectivePolicyRejectsUnknownReviewMode(t *testing.T) {
	root := policyFixture(t, "", "", "overrides:\n  branch_protection:\n    review_mode: unreviewed\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err == nil || !strings.Contains(err.Error(), "unsupported branch protection review mode") {
		t.Fatalf("effective policy accepted unknown review mode: %v", err)
	}
}
