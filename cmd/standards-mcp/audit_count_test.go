package main

import (
	"fmt"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// auditAllPassed is the summary count a clean fixture audit prints: every gate of auditGates plus the manifest
// gate. It is derived from the gate list, so a new gate updates every audit assertion at once (order
// praetor-rules-24h-a11y2-1682-land-fail-20261029: the A11Y2 UI gate left ten tests asserting 15/15).
func auditAllPassed() string {
	n := len((&Server{}).auditGates(auditPaths{}, &config.EffectivePolicy{})) + 1
	return fmt.Sprintf("passed: %d/%d", n, n)
}

// TestAuditAllPassed_Boundary_CountsEveryGate pins the derived count, so a gate added or dropped is a visible
// change of this one line rather than of every audit test.
func TestAuditAllPassed_Boundary_CountsEveryGate(t *testing.T) {
	if got := auditAllPassed(); got != "passed: 16/16" {
		t.Fatalf("auditAllPassed() = %q, want %q (one per auditGates entry plus the manifest gate)", got, "passed: 16/16")
	}
}
