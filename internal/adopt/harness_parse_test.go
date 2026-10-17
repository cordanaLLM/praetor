package adopt

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestHarnessDirectives_ParseGatedInvariants: the invariant table of an adopted AGENTS.md is
// read by the same parser as praetor's own (hisscatalog.ParseGatedInvariants, which feeds the
// generated wiki), so every catalog rule parses back with the cells the harness wrote.
func TestHarnessDirectives_ParseGatedInvariants(t *testing.T) {
	facts := adoptedFacts("owner", "repo", "praetor", &VerificationPlan{})
	content, err := buildAgentHarness(facts)
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
		generated, failure := rule.AdoptedFor(facts.pipelines, facts.hiss)
		check := harnessEnforcement(rule, facts, generated)
		want := hisscatalog.GatedInvariant{ID: rule.ID, Scope: rule.Scope, Rule: rule.AdoptedDirective(repositoryFacts(nil, 0)), Enforcement: check, OnFail: failure}
		if gated[i] != want {
			t.Errorf("row %d parsed as %+v, want %+v", i, gated[i], want)
		}
	}
}
