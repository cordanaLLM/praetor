package adopt

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestHarnessDirectives_ParseGatedInvariants: the invariant table of an adopted AGENTS.md is
// read by the same parser as praetor's own (hisscatalog.ParseGatedInvariants, which feeds the
// generated wiki), so every catalog rule parses back with the cells the harness wrote.
func TestHarnessDirectives_ParseGatedInvariants(t *testing.T) {
	content, err := buildAgentHarness(adoptedFacts("owner", "repo", "praetor", &VerificationPlan{}))
	if err != nil {
		t.Fatalf("buildAgentHarness failed: %v", err)
	}
	gated, err := hisscatalog.ParseGatedInvariants(content)
	if err != nil {
		t.Fatalf("ParseGatedInvariants failed on full harness output: %v", err)
	}
	rules := hisscatalog.Rules()
	if len(gated) != len(rules) {
		t.Fatalf("parsed %d invariants from the harness, catalog has %d", len(gated), len(rules))
	}
	for i, rule := range rules {
		check, failure := rule.Adopted(hisscatalog.AllPipelines)
		want := hisscatalog.GatedInvariant{ID: rule.ID, Scope: rule.Scope, Rule: rule.AdoptedDirective(hisscatalog.Facts{}), Enforcement: check, OnFail: failure}
		if gated[i] != want {
			t.Errorf("row %d parsed as %+v, want %+v", i, gated[i], want)
		}
	}
}
