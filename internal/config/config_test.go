package config

import (
	"testing"
)

func TestJoinLattice_HighestStandardWins(t *testing.T) {
	policyA := &ResolvedPolicy{
		Complexity: ComplexityPolicy{
			MaxCyclomatic: 15,
			MaxCognitive:  20,
			MaxFuncLOC:    100,
			MaxStatements: 60,
		},
		BranchProtection: BranchProtectionPolicy{
			EnforceLinearHistory:       false,
			RequireSignedCommits:       false,
			RequiredApprovingReviewers: 1,
			DismissStaleReviews:        false,
		},
		SupplyChain: SupplyChainPolicy{
			SLSALevel:     1,
			EnforceCosign: false,
			RequireSBOM:   false,
		},
		Linters:     []string{"govet"},
		DevFeatures: []string{"go"},
	}

	policyB := &ResolvedPolicy{
		Complexity: ComplexityPolicy{
			MaxCyclomatic: 10,
			MaxCognitive:  12,
			MaxFuncLOC:    75,
			MaxStatements: 40,
		},
		BranchProtection: BranchProtectionPolicy{
			EnforceLinearHistory:       true,
			RequireSignedCommits:       true,
			RequiredApprovingReviewers: 2,
			DismissStaleReviews:        true,
		},
		SupplyChain: SupplyChainPolicy{
			SLSALevel:     3,
			EnforceCosign: true,
			RequireSBOM:   true,
		},
		Linters:     []string{"semgrep", "gitleaks"},
		DevFeatures: []string{"rust", "common-utils"},
	}

	joined := Join(policyA, policyB)

	// Invariants: Min complexity bounds win
	if joined.Complexity.MaxCyclomatic != 10 {
		t.Fatalf("expected max cyclomatic 10, got %d", joined.Complexity.MaxCyclomatic)
	}
	if joined.Complexity.MaxFuncLOC != 75 {
		t.Fatalf("expected max func loc 75, got %d", joined.Complexity.MaxFuncLOC)
	}

	// Invariants: Strictest branch protection wins
	if !joined.BranchProtection.EnforceLinearHistory {
		t.Fatalf("expected linear history true")
	}
	if !joined.BranchProtection.RequireSignedCommits {
		t.Fatalf("expected signed commits true")
	}
	if joined.BranchProtection.RequiredApprovingReviewers != 2 {
		t.Fatalf("expected 2 approving reviewers, got %d", joined.BranchProtection.RequiredApprovingReviewers)
	}

	// Invariants: Supply chain max level wins
	if joined.SupplyChain.SLSALevel != 3 {
		t.Fatalf("expected SLSA level 3, got %d", joined.SupplyChain.SLSALevel)
	}
	if !joined.SupplyChain.EnforceCosign {
		t.Fatalf("expected Cosign true")
	}

	// Invariants: Linters and DevFeatures are unions
	if len(joined.Linters) != 3 {
		t.Fatalf("expected 3 linters, got %d", len(joined.Linters))
	}
	if len(joined.DevFeatures) != 3 {
		t.Fatalf("expected 3 dev features, got %d", len(joined.DevFeatures))
	}
}

func TestLoadManifest_Dogfood(t *testing.T) {
	manifest, err := LoadManifest("../../.standards.yaml")
	if err != nil {
		t.Fatalf("failed to load root .standards.yaml: %v", err)
	}

	if manifest.Repository.Name != "praetor" && manifest.Repository.Name != "standards" {
		t.Fatalf("expected repo name 'praetor' or 'standards', got '%s'", manifest.Repository.Name)
	}
	if len(manifest.Profiles) == 0 {
		t.Fatalf("expected at least 1 profile")
	}
	if len(manifest.Facets) == 0 {
		t.Fatalf("expected at least 1 facet")
	}
}
