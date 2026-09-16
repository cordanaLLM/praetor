package baseline

import "testing"

func inf(file, rule string, line int) Infraction {
	return Infraction{RuleID: rule, FilePath: file, LineNumber: line,
		Fingerprint: file + ":" + itoa(line) + ":" + rule}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// Positive, and the case that motivated the option: a mechanical change touches a file carrying
// baselined debt without adding any. Under the default rule this fails; under DebtDelta it passes.
func TestEvaluateRatchetWithOptions_Positive_MechanicalChangePassesUnderDebtDelta(t *testing.T) {
	recorded := []Infraction{inf("cmd/run.go", "HISS-04", 78)}
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: recorded}
	current := []Infraction{inf("cmd/run.go", "HISS-04", 78)}
	touched := []string{"cmd/run.go"}

	if EvaluateRatchet(b, current, touched).Passed {
		t.Fatal("precondition: the default boy-scout rule must still fail a touched file with debt")
	}
	res := EvaluateRatchetWithOptions(b, current, touched, RatchetOptions{DebtDelta: true})
	if !res.Passed {
		t.Fatalf("a debt-neutral change to a touched file failed under DebtDelta: %+v", res)
	}
}

// The case a fingerprint comparison gets wrong. A fingerprint is path:line:rule, so adding a line
// above a baselined infraction changes its fingerprint without changing the debt. Counting per
// file and rule is what makes this pass.
func TestEvaluateRatchetWithOptions_Positive_ALineShiftIsNotNewDebt(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{inf("cmd/run.go", "HISS-04", 78)}}
	shifted := []Infraction{inf("cmd/run.go", "HISS-04", 79)}
	res := EvaluateRatchetWithOptions(b, shifted, []string{"cmd/run.go"}, RatchetOptions{DebtDelta: true})
	if !res.Passed {
		t.Fatalf("a one-line shift was read as new debt: %+v", res)
	}
}

// Negative: DebtDelta relaxes the rule for debt-neutral files only. A touched file that gained an
// infraction still fails, and is still reported as a touched-file violation.
func TestEvaluateRatchetWithOptions_Negative_AWorsenedTouchedFileStillFails(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{inf("cmd/run.go", "HISS-04", 78)}}
	current := []Infraction{inf("cmd/run.go", "HISS-04", 78), inf("cmd/run.go", "HISS-04", 140)}
	res := EvaluateRatchetWithOptions(b, current, []string{"cmd/run.go"}, RatchetOptions{DebtDelta: true})
	if res.Passed {
		t.Fatal("a touched file whose debt grew passed under DebtDelta")
	}
	if len(res.TouchedCleanViolations) == 0 {
		t.Error("the worsened file is not reported as a touched-file violation")
	}
}

// Negative: swapping one rule's infraction for another's is not debt-neutral. The total is
// unchanged, but HISS-07 is new in this file, and a lower-or-equal total must not hide it.
func TestEvaluateRatchetWithOptions_Negative_ARuleSwapIsNewDebt(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{inf("cmd/run.go", "HISS-04", 78)}}
	swapped := []Infraction{inf("cmd/run.go", "HISS-07", 78)}
	res := EvaluateRatchetWithOptions(b, swapped, []string{"cmd/run.go"}, RatchetOptions{DebtDelta: true})
	if res.Passed {
		t.Fatal("trading one rule's infraction for another's was accepted as debt-neutral")
	}
}

// Boundary: an untouched file is judged exactly as before -- DebtDelta changes only touched
// files -- and a touched file with no debt at all passes either way.
func TestEvaluateRatchetWithOptions_Boundary_UntouchedAndCleanFiles(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 0}
	newInUntouched := []Infraction{inf("other.go", "HISS-02", 9)}
	if EvaluateRatchetWithOptions(b, newInUntouched, []string{"cmd/run.go"}, RatchetOptions{DebtDelta: true}).Passed {
		t.Error("new debt in an untouched file passed under DebtDelta")
	}
	if !EvaluateRatchetWithOptions(b, nil, []string{"cmd/run.go"}, RatchetOptions{DebtDelta: true}).Passed {
		t.Error("a clean touched file failed")
	}
}
