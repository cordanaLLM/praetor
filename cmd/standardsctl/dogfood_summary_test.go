package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

// Positive: a passing context sync is reported without naming a fixed set of vendor files,
// since agent_clients decides which projections exist.
func TestPrintDogfoodSummary_Positive_ContextSyncNamesNoFixedVendors(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printDogfoodSummary(&dogfood.DogfoodReport{ContextSyncPassed: true, SelfAuditPassed: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[PASS] Cross-agent context targets in sync with AGENTS.md (the projections agent_clients selects)")
	for _, vendor := range []string{"CLAUDE", "Cursor", "Gemini", "Codex"} {
		if strings.Contains(out, vendor) {
			t.Errorf("summary claims the %s projection: %s", vendor, out)
		}
	}
}

// Negative: a failed context sync still reports FAIL, not PASS.
func TestPrintDogfoodSummary_Negative_ContextSyncFailure(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printDogfoodSummary(&dogfood.DogfoodReport{})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[FAIL] Context targets out of sync")
	if strings.Contains(out, "[PASS] Cross-agent") {
		t.Fatalf("failed sync reported as passing: %s", out)
	}
}

// Boundary: with no local targets the simulation section is omitted entirely.
func TestPrintDogfoodSummary_Boundary_NoTargetsOmitsSimulations(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printDogfoodSummary(&dogfood.DogfoodReport{ContextSyncPassed: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Local Target Adoption Simulations") {
		t.Fatalf("empty target list printed a simulation header: %s", out)
	}
}
