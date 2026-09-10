package baseline

import (
	"testing"
)

func TestEvaluateRatchet(t *testing.T) {
	b := &Baseline{
		Version:          1,
		TotalInfractions: 2,
		Infractions: []Infraction{
			{RuleID: "HISS-04", FilePath: "legacy/old.go", LineNumber: 10, Fingerprint: "fp1"},
			{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
		},
	}

	// Case 1: Untouched legacy files pass cleanly without new violations
	current := []Infraction{
		{RuleID: "HISS-04", FilePath: "legacy/old.go", LineNumber: 10, Fingerprint: "fp1"},
		{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
	}
	res := EvaluateRatchet(b, current, []string{"new_feature.go"})
	if !res.Passed {
		t.Fatalf("expected untouched legacy to pass")
	}

	// Case 2: New violation introduced in new file -> Fails
	currentWithNew := append(current, Infraction{
		RuleID:      "HISS-07",
		FilePath:    "new_feature.go",
		LineNumber:  50,
		Fingerprint: "fp3",
	})
	resFail := EvaluateRatchet(b, currentWithNew, []string{"new_feature.go"})
	if resFail.Passed {
		t.Fatalf("expected new violation to fail")
	}
	if len(resFail.TouchedCleanViolations) != 1 {
		t.Fatalf("expected 1 touched clean violation, got %d", len(resFail.TouchedCleanViolations))
	}

	// Case 3: Touched-File Clean Rule: Modifying legacy/old.go revokes exemption -> Fails
	resTouchedLegacy := EvaluateRatchet(b, current, []string{"legacy/old.go"})
	if resTouchedLegacy.Passed {
		t.Fatalf("expected touched legacy file to fail until refactored clean")
	}
	if len(resTouchedLegacy.TouchedCleanViolations) != 1 {
		t.Fatalf("expected 1 touched clean violation for touched legacy file")
	}

	// Case 4: Developer refactored legacy/old.go clean; debt decreased -> Passes
	currentRefactored := []Infraction{
		{RuleID: "HISS-02", FilePath: "legacy/timer.go", LineNumber: 25, Fingerprint: "fp2"},
	}
	resDebtDecreased := EvaluateRatchet(b, currentRefactored, []string{"legacy/old.go"})
	if !resDebtDecreased.Passed {
		t.Fatalf("expected debt decrease with refactored file to pass")
	}
	if resDebtDecreased.CurrentCount >= resDebtDecreased.PreviousCount {
		t.Fatalf("expected debt count to decrease")
	}
}
