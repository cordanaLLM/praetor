package baseline

import (
	"strings"
	"testing"
)

// touchedFixture is a baseline recording two findings of one file and a scan of that file that
// adds a third, so the touched-file rule lists one finding the baseline does not record and two
// it does (#348).
func touchedFixture() (*Baseline, []Infraction) {
	recorded := []Infraction{inf("core/filter.c", "HISS-04", 10), inf("core/filter.c", "HISS-04", 40)}
	current := []Infraction{inf("core/filter.c", "HISS-04", 10), inf("core/filter.c", "HISS-04", 40), inf("core/filter.c", "HISS-04", 70)}
	return &Baseline{Version: 1, TotalInfractions: len(recorded), Infractions: recorded}, current
}

// Positive (#348): the touched count is split into findings the baseline does not record and
// baselined ones, the unrecorded ones are listed first, and the explanation says the baselined
// ones fail too under the default rule and what clears them.
func TestRatchetResultSummary_Positive_TouchedCountSplitsBaselined(t *testing.T) {
	b, current := touchedFixture()
	res := EvaluateRatchet(b, current, []string{"core/filter.c"})
	if res.Passed || len(res.TouchedBaselined) != 3 || res.TouchedBaselined[2] {
		t.Fatalf("evaluation = %+v; want a rejection marking the third touched finding unbaselined", res)
	}
	got := res.Summary()
	for _, want := range []string{
		"3 in touched files (1 not in the baseline, 2 baselined)",
		"[HISS-04] core/filter.c:70 -  (touched file must be clean, not in the baseline)",
		"[HISS-04] core/filter.c:10 -  (touched file must be clean, baselined)",
		"2 of the touched-file violations are baselined: touching a file revokes its baseline exemptions, so they fail too",
		debtDeltaFlag,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "core/filter.c:70") > strings.Index(got, "core/filter.c:10") {
		t.Errorf("the finding the baseline does not record is not listed first:\n%s", got)
	}
}

// Positive (#348): under DebtDelta the baselined findings of a worsened file fail because its
// count rose, and the explanation says so instead of naming the debt-delta flag.
func TestRatchetResultSummary_Positive_TouchedExplanationUnderDebtDelta(t *testing.T) {
	b, current := touchedFixture()
	res := EvaluateRatchetWithOptions(b, current, []string{"core/filter.c"}, RatchetOptions{DebtDelta: true})
	if res.Passed || !res.DebtDelta {
		t.Fatalf("evaluation = %+v; want a debt-delta rejection", res)
	}
	got := res.Summary()
	if !strings.Contains(got, "2 of the touched-file violations are baselined: they fail because a rule's count in their file rose") ||
		strings.Contains(got, debtDeltaFlag) {
		t.Errorf("debt-delta explanation wrong:\n%s", got)
	}
}

// Negative (#348): without a baselined mark for every touched finding, as for a result built by
// hand or decoded from an older writer, the touched count stays one figure, one class with the
// plain tag, and no explanation.
func TestRatchetResultSummary_Negative_UnmarkedTouchedStaysOneFigure(t *testing.T) {
	for _, marks := range [][]bool{nil, {true}} {
		res := &RatchetResult{PreviousCount: 2, CurrentCount: 2, TouchedBaselined: marks,
			TouchedCleanViolations: []Infraction{inf("a.go", "HISS-04", 1), inf("a.go", "HISS-04", 9)}}
		got := res.Summary()
		if !strings.Contains(got, "2 in touched files):") || strings.Contains(got, " baselined") || strings.Contains(got, "not in the baseline") ||
			strings.Count(got, "(touched file must be clean)") != 2 {
			t.Errorf("marks %v: unmarked touched findings were split:\n%s", marks, got)
		}
	}
}

// Boundary (#348): each touched class is bounded and counted on its own, one over the bound hides
// exactly one in the singular, and a split with nothing touched keeps the bare zero.
func TestRatchetResultSummary_Boundary_TouchedClassesBoundedApart(t *testing.T) {
	res := &RatchetResult{PreviousCount: 8, CurrentCount: 8}
	for i := 1; i <= maxDescribedViolations+1; i++ {
		res.TouchedCleanViolations = append(res.TouchedCleanViolations, inf("a.go", "HISS-04", i), inf("b.go", "HISS-04", i))
		res.TouchedBaselined = append(res.TouchedBaselined, false, true)
	}
	got := res.Summary()
	for _, want := range []string{
		"8 in touched files (4 not in the baseline, 4 baselined)",
		"... and 1 more touched-file violation not in the baseline not shown; " + auditLister,
		"... and 1 more baselined touched-file violation not shown; " + auditLister,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() missing %q:\n%s", want, got)
		}
	}
	empty := &RatchetResult{PreviousCount: 0, CurrentCount: 1, TouchedBaselined: []bool{},
		NewViolations: []Infraction{inf("c.go", "HISS-07", 3)}}
	if got := empty.Summary(); !strings.Contains(got, "0 in touched files):") {
		t.Errorf("an empty touched split is not the bare zero:\n%s", got)
	}
}

// Positive (#349): baseline entries a cleanup removed without a re-record are counted per file
// and rule, the ratchet still passes, and the notice names the count and the re-record.
func TestEvaluateRatchet_Positive_StaleEntriesCounted(t *testing.T) {
	var recorded []Infraction
	for i := 1; i <= 14; i++ {
		recorded = append(recorded, inf("core/cleaned.cpp", "HISS-04", i))
	}
	for i := 1; i <= 5; i++ {
		recorded = append(recorded, inf("test/cleaned_test.c", "HISS-02", i))
	}
	kept := inf("core/kept.go", "HISS-07", 3)
	recorded = append(recorded, kept)
	b := &Baseline{Version: 1, TotalInfractions: len(recorded), Infractions: recorded}

	res := EvaluateRatchet(b, []Infraction{kept}, nil)
	if !res.Passed || res.Stale != 19 {
		t.Fatalf("evaluation = passed %t stale %d; want a pass with 19 stale entries", res.Passed, res.Stale)
	}
	want := "19 baseline entries match nothing in the tree: per file and rule the baseline records more infractions than the scan found"
	if got := res.StaleNotice(); !strings.Contains(got, want) || !strings.Contains(got, reRecord) {
		t.Errorf("StaleNotice() = %q, want %q and %s", got, want, reRecord)
	}
}

// Negative (#349): a line shift above a baselined finding changes its fingerprint, not the debt,
// so it leaves no stale entry; a new finding elsewhere does not offset one that went away; and a
// nil result has no notice.
func TestEvaluateRatchet_Negative_LineShiftIsNotStale(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 2, Infractions: []Infraction{inf("a.go", "HISS-04", 10), inf("b.go", "HISS-04", 5)}}
	shifted := EvaluateRatchetWithOptions(b, []Infraction{inf("a.go", "HISS-04", 12), inf("b.go", "HISS-04", 5)},
		[]string{"a.go"}, RatchetOptions{DebtDelta: true})
	if !shifted.Passed || shifted.Stale != 0 || shifted.StaleNotice() != "" {
		t.Errorf("line shift: passed %t stale %d notice %q; want a pass with nothing stale", shifted.Passed, shifted.Stale, shifted.StaleNotice())
	}
	swapped := EvaluateRatchet(b, []Infraction{inf("a.go", "HISS-04", 10), inf("c.go", "HISS-04", 1)}, nil)
	if swapped.Stale != 1 {
		t.Errorf("a finding that went away was offset by one elsewhere: stale %d, want 1", swapped.Stale)
	}
	var nilResult *RatchetResult
	if got := nilResult.StaleNotice(); got != "" {
		t.Errorf("nil StaleNotice() = %q", got)
	}
}

// Boundary (#349): an empty baseline, an exact match and a scan that found more than the baseline
// records have nothing stale; exactly one stale entry is named in the singular.
func TestEvaluateRatchet_Boundary_StaleCount(t *testing.T) {
	one := inf("a.go", "HISS-04", 1)
	cases := []struct {
		name     string
		recorded []Infraction
		current  []Infraction
		want     int
	}{
		{"empty", nil, nil, 0},
		{"exact", []Infraction{one}, []Infraction{one}, 0},
		{"grown", []Infraction{one}, []Infraction{one, inf("a.go", "HISS-04", 2)}, 0},
		{"one gone", []Infraction{one, inf("a.go", "HISS-04", 2)}, []Infraction{one}, 1},
	}
	for _, tc := range cases {
		b := &Baseline{Version: 1, TotalInfractions: len(tc.recorded), Infractions: tc.recorded}
		if got := EvaluateRatchet(b, tc.current, nil).Stale; got != tc.want {
			t.Errorf("%s: stale %d, want %d", tc.name, got, tc.want)
		}
	}
	res := &RatchetResult{Stale: 1}
	if got := res.StaleNotice(); !strings.HasPrefix(got, "1 baseline entry matches nothing in the tree") {
		t.Errorf("singular StaleNotice() = %q", got)
	}
	if got := (&RatchetResult{}).StaleNotice(); got != "" {
		t.Errorf("zero stale StaleNotice() = %q", got)
	}
}

// named is inf with a symbol and a message, the line-independent identity a scan reports.
func named(file, rule string, line int, symbol, message string) Infraction {
	v := inf(file, rule, line)
	v.Symbol, v.Message = symbol, message
	return v
}

// shiftFixture records five findings of one file, each with its own symbol, and returns the scan
// after one line was inserted at the top: every fingerprint changed, the debt did not.
func shiftFixture() (*Baseline, []Infraction) {
	var recorded, shifted []Infraction
	for i := 1; i <= 5; i++ {
		recorded = append(recorded, named("x.c", "HISS-04", i*10, "f"+itoa(i), "too complex"))
		shifted = append(shifted, named("x.c", "HISS-04", i*10+1, "f"+itoa(i), "too complex"))
	}
	return &Baseline{Version: 1, TotalInfractions: len(recorded), Infractions: recorded}, shifted
}

// Positive (#348): a line shift above recorded debt changes fingerprints, not the debt, so under
// the default rule every touched finding still counts as baselined; with one genuinely new
// finding under DebtDelta only that one is not in the baseline, and the listing names it.
func TestRatchetResultSummary_Positive_TouchedLineShiftStaysBaselined(t *testing.T) {
	b, shifted := shiftFixture()
	got := EvaluateRatchet(b, shifted, []string{"x.c"}).Summary()
	if !strings.Contains(got, "5 in touched files (0 not in the baseline, 5 baselined)") ||
		!strings.Contains(got, "5 of the touched-file violations are baselined: touching a file revokes") {
		t.Errorf("line shift under the default rule:\n%s", got)
	}

	grown := append([]Infraction{named("x.c", "HISS-04", 2, "g", "too complex")}, shifted...)
	got = EvaluateRatchetWithOptions(b, grown, []string{"x.c"}, RatchetOptions{DebtDelta: true}).Summary()
	for _, want := range []string{
		"6 in touched files (1 not in the baseline, 5 baselined)",
		"[HISS-04] x.c:2 - too complex (touched file must be clean, not in the baseline)",
		"[HISS-04] x.c:11 - too complex (touched file must be clean, baselined)",
		"5 of the touched-file violations are baselined: they fail because a rule's count in their file rose",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("line shift plus one new finding under DebtDelta missing %q:\n%s", want, got)
		}
	}
}

// Negative (#348): recorded debt accounts only for findings of its own file and rule, so a
// finding of another rule in the same file, or of the same rule in another file, is not in the
// baseline however much room the baseline records elsewhere.
func TestRatchetResultSummary_Negative_TouchedRoomIsPerFileAndRule(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 2, Infractions: []Infraction{inf("x.c", "HISS-04", 10), inf("y.c", "HISS-07", 10)}}
	current := []Infraction{inf("x.c", "HISS-07", 11), inf("y.c", "HISS-04", 11)}
	got := EvaluateRatchet(b, current, []string{"x.c", "y.c"}).Summary()
	if !strings.Contains(got, "2 in touched files (2 not in the baseline, 0 baselined)") || strings.Contains(got, "are baselined") {
		t.Errorf("room crossed a file or rule:\n%s", got)
	}
}

// Boundary (#348): a recorded finding whose message changed with the shift (its measured value
// rose) still takes its recorded slot; exactly as many findings as the baseline records stay
// baselined, and one more is the single finding not in the baseline.
func TestRatchetResultSummary_Boundary_TouchedSlotsMatchRecordedCount(t *testing.T) {
	b := &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{named("x.c", "HISS-04", 10, "f", "complexity 12")}}
	changed := []Infraction{named("x.c", "HISS-04", 11, "f", "complexity 13")}
	if got := EvaluateRatchet(b, changed, []string{"x.c"}).Summary(); !strings.Contains(got, "1 in touched files (0 not in the baseline, 1 baselined)") {
		t.Errorf("a changed message lost its recorded slot:\n%s", got)
	}
	plusOne := append(changed, named("x.c", "HISS-04", 30, "h", "complexity 11"))
	res := EvaluateRatchet(b, plusOne, []string{"x.c"})
	if got := res.Summary(); !strings.Contains(got, "2 in touched files (1 not in the baseline, 1 baselined)") {
		t.Errorf("one over the recorded count:\n%s", got)
	}
	if len(res.TouchedBaselined) != 2 || !res.TouchedBaselined[0] || res.TouchedBaselined[1] {
		t.Errorf("marks = %v; want the recorded symbol baselined and the new one not", res.TouchedBaselined)
	}
}
