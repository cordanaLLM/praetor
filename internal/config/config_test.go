package config

import (
	"testing"
)

func createLatticePolicies() (*ResolvedPolicy, *ResolvedPolicy) {
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
	return policyA, policyB
}

func TestJoinLattice_HighestStandardWins(t *testing.T) {
	policyA, policyB := createLatticePolicies()
	joined := Join(policyA, policyB)

	// Invariants: Min complexity bounds win
	if joined.Complexity.MaxCyclomatic != 10 || joined.Complexity.MaxFuncLOC != 75 {
		t.Fatalf("unexpected complexity bounds: %+v", joined.Complexity)
	}

	// Invariants: Strictest branch protection wins
	if !joined.BranchProtection.EnforceLinearHistory || !joined.BranchProtection.RequireSignedCommits || joined.BranchProtection.RequiredApprovingReviewers != 2 {
		t.Fatalf("unexpected branch protection: %+v", joined.BranchProtection)
	}

	// Invariants: Supply chain max level wins
	if joined.SupplyChain.SLSALevel != 3 || !joined.SupplyChain.EnforceCosign {
		t.Fatalf("unexpected supply chain: %+v", joined.SupplyChain)
	}

	// Invariants: Linters and DevFeatures are unions
	if len(joined.Linters) != 3 || len(joined.DevFeatures) != 3 {
		t.Fatalf("unexpected linters or features count: linters=%d features=%d", len(joined.Linters), len(joined.DevFeatures))
	}
}

// The manifest tests live in strict_manifest_test.go: hermetic fixtures with exact
// assertions, the KnownFields refusals, the empty-document boundary and one guarded read
// of this repository's own manifest. A second copy here read the live checkout and
// accepted either of two repository names, which is not an assertion.
