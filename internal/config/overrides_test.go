package config

import "testing"

func TestApplyOverrides_Positive_TightensEveryDimension(t *testing.T) {
	p := DefaultPolicy()
	p.ApplyOverrides(Overrides{
		Complexity:       &ComplexityPolicy{MaxCyclomatic: 5, MaxCognitive: 7, MaxFuncLOC: 40, MaxStatements: 20},
		BranchProtection: &BranchProtectionPolicy{RequireSignedCommits: true, RequiredApprovingReviewers: 3},
		SupplyChain:      &SupplyChainPolicy{SLSALevel: 3, EnforceCosign: true, RequireSBOM: true},
	})
	if p.Complexity.MaxCyclomatic != 5 || p.Complexity.MaxCognitive != 7 || p.Complexity.MaxFuncLOC != 40 || p.Complexity.MaxStatements != 20 {
		t.Fatalf("complexity overrides not applied: %+v", p.Complexity)
	}
	if !p.BranchProtection.RequireSignedCommits || p.BranchProtection.RequiredApprovingReviewers != 3 || !p.BranchProtection.EnforceLinearHistory {
		t.Fatalf("branch protection overrides not applied or defaults lost: %+v", p.BranchProtection)
	}
	if p.SupplyChain.SLSALevel != 3 || !p.SupplyChain.EnforceCosign || !p.SupplyChain.RequireSBOM {
		t.Fatalf("supply chain overrides not applied: %+v", p.SupplyChain)
	}
}

func TestApplyOverrides_Negative_CannotLoosen(t *testing.T) {
	p := DefaultPolicy()
	base := *p
	p.ApplyOverrides(Overrides{
		Complexity:       &ComplexityPolicy{MaxCyclomatic: 99, MaxCognitive: 99, MaxFuncLOC: 999, MaxStatements: 999},
		BranchProtection: &BranchProtectionPolicy{EnforceLinearHistory: false, DismissStaleReviews: false, RequiredApprovingReviewers: 0},
		SupplyChain:      &SupplyChainPolicy{SLSALevel: 0, EnforceCosign: false, RequireSBOM: false},
	})
	if p.Complexity != base.Complexity {
		t.Fatalf("looser complexity caps must be ignored: got %+v want %+v", p.Complexity, base.Complexity)
	}
	if p.BranchProtection != base.BranchProtection {
		t.Fatalf("branch protection must never weaken: got %+v want %+v", p.BranchProtection, base.BranchProtection)
	}
	if p.SupplyChain != base.SupplyChain {
		t.Fatalf("supply chain must never weaken: got %+v want %+v", p.SupplyChain, base.SupplyChain)
	}
}

func TestApplyOverrides_Boundary_NilSectionsAndZeroValues(t *testing.T) {
	p := DefaultPolicy()
	base := *p
	p.ApplyOverrides(Overrides{})
	if p.Complexity != base.Complexity || p.BranchProtection != base.BranchProtection || p.SupplyChain != base.SupplyChain {
		t.Fatalf("empty overrides must be a no-op: got %+v want %+v", *p, base)
	}
	p.ApplyOverrides(Overrides{Complexity: &ComplexityPolicy{}})
	if p.Complexity != base.Complexity {
		t.Fatalf("zero-valued complexity caps must be ignored: got %+v", p.Complexity)
	}
	// Equal values are accepted (idempotent).
	p.ApplyOverrides(Overrides{Complexity: &ComplexityPolicy{MaxCyclomatic: base.Complexity.MaxCyclomatic}})
	if p.Complexity.MaxCyclomatic != base.Complexity.MaxCyclomatic {
		t.Fatalf("equal cap must be kept, got %d", p.Complexity.MaxCyclomatic)
	}
}
