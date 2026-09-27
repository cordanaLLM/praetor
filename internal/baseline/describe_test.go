package baseline

import (
	"strings"
	"testing"
)

// Summary is the one renderer for a ratchet rejection, shared by `praetorctl audit` and the
// gate's HISS stage. It must name every listed violation by rule, file and line.
func TestRatchetResultSummary_Positive_NamesFileLineAndRule(t *testing.T) {
	res := &RatchetResult{
		PreviousCount:          1,
		CurrentCount:           2,
		NewViolations:          []Infraction{{RuleID: "HISS-07", FilePath: "pkg/new.go", LineNumber: 12, Message: "panic"}},
		TouchedCleanViolations: []Infraction{{RuleID: "HISS-04", FilePath: "pkg/old.go", LineNumber: 3, Message: "too long"}},
	}
	got := res.Summary()
	for _, want := range []string{
		"2 total infractions, 1 new unbaselined, 1 in touched files",
		"[HISS-07] pkg/new.go:12 - panic (new)",
		"[HISS-04] pkg/old.go:3 - too long (touched file must be clean)",
		"total infractions rose from 1 to 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() missing %q:\n%s", want, got)
		}
	}
}

func TestRatchetResultSummary_Negative_NilResult(t *testing.T) {
	var res *RatchetResult
	if got := res.Summary(); got != "no ratchet result" {
		t.Errorf("nil Summary() = %q", got)
	}
}

// Boundary: the listing is bounded per class, and a count that did not rise is not claimed.
func TestRatchetResultSummary_Boundary_ListsAtMostThreePerClass(t *testing.T) {
	res := &RatchetResult{PreviousCount: 5, CurrentCount: 5}
	for i := 1; i <= maxDescribedViolations+2; i++ {
		res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-02", FilePath: "a.go", LineNumber: i})
	}
	got := res.Summary()
	if n := strings.Count(got, "(new)"); n != maxDescribedViolations {
		t.Errorf("listed %d new violations, want %d:\n%s", n, maxDescribedViolations, got)
	}
	if !strings.Contains(got, "5 new unbaselined") || strings.Contains(got, "rose from") {
		t.Errorf("unexpected summary:\n%s", got)
	}
}

// Positive (BUG-489): a count-only regression names both totals rather than an empty
// violation listing that reads as "0 new".
func TestRatchetResultSummary_Positive_CountRegressedNamesTotals(t *testing.T) {
	res := &RatchetResult{CountRegressed: true, PreviousCount: 5, CurrentCount: 7}
	got := res.Summary()
	if !strings.Contains(got, "total infractions rose from 5 to 7") || strings.Contains(got, "0 new unbaselined") {
		t.Errorf("count-only regression summary = %q", got)
	}
}
