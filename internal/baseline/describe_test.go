package baseline

import (
	"slices"
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
		Attribution:            []Attribution{AttributionIntroduced},
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

// Boundary: the listing is bounded per class, a cut class says how many lines it hid and which
// read-only command lists them (#598), and a count that did not rise is not claimed.
func TestRatchetResultSummary_Boundary_ListsAtMostThreePerClass(t *testing.T) {
	res := &RatchetResult{PreviousCount: 5, CurrentCount: 5}
	for i := 1; i <= maxDescribedViolations+2; i++ {
		res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-02", FilePath: "a.go", LineNumber: i})
		res.Attribution = append(res.Attribution, AttributionIntroduced)
	}
	got := res.Summary()
	if n := strings.Count(got, "(new)"); n != maxDescribedViolations {
		t.Errorf("listed %d new violations, want %d:\n%s", n, maxDescribedViolations, got)
	}
	if !strings.Contains(got, "5 new unbaselined") || strings.Contains(got, "rose from") {
		t.Errorf("unexpected summary:\n%s", got)
	}
	marker := "... and 2 more new violations not shown; " + verifyLister + " lists every one"
	if !strings.Contains(got, marker) {
		t.Errorf("Summary() does not say how many it hid, want %q:\n%s", marker, got)
	}
}

// Boundary (#598): one line over the bound hides exactly one, in the singular, and the touched
// class is counted apart from the new class, each naming the command that lists it.
func TestRatchetResultSummary_Boundary_MarkerCountsEachClass(t *testing.T) {
	res := &RatchetResult{PreviousCount: 0, CurrentCount: 9}
	for i := 1; i <= maxDescribedViolations+1; i++ {
		res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-01", FilePath: "a.py", LineNumber: i})
		res.Attribution = append(res.Attribution, AttributionIntroduced)
	}
	for i := 1; i <= maxDescribedViolations+2; i++ {
		res.TouchedCleanViolations = append(res.TouchedCleanViolations, Infraction{RuleID: "HISS-04", FilePath: "b.go", LineNumber: i})
	}
	got := res.Summary()
	for _, want := range []string{
		"... and 1 more new violation not shown; " + verifyLister,
		"... and 2 more touched-file violations not shown; " + auditLister,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() missing %q:\n%s", want, got)
		}
	}
}

// Negative (#598): a class within the bound prints no marker, and FullSummary never does.
func TestRatchetResultSummary_Negative_NoMarkerWithinBound(t *testing.T) {
	res := &RatchetResult{PreviousCount: 0, CurrentCount: maxDescribedViolations}
	for i := 1; i <= maxDescribedViolations; i++ {
		res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-02", FilePath: "a.go", LineNumber: i})
	}
	if got := res.Summary(); strings.Contains(got, "not shown") {
		t.Errorf("a listing within the bound claims hidden lines:\n%s", got)
	}
	res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-02", FilePath: "a.go", LineNumber: 99})
	if got := res.FullSummary(); strings.Contains(got, "not shown") {
		t.Errorf("FullSummary() hid lines:\n%s", got)
	}
}

// Positive (#598): FullSummary lists every violation of every class.
func TestRatchetResultFullSummary_Positive_ListsEveryViolation(t *testing.T) {
	res := &RatchetResult{PreviousCount: 0, CurrentCount: 12}
	for i := 1; i <= 2*maxDescribedViolations; i++ {
		res.NewViolations = append(res.NewViolations, Infraction{RuleID: "HISS-01", FilePath: "a.py", LineNumber: i})
		res.TouchedCleanViolations = append(res.TouchedCleanViolations, Infraction{RuleID: "HISS-04", FilePath: "b.go", LineNumber: i})
	}
	got := res.FullSummary()
	if n := strings.Count(got, "(not in the baseline)"); n != 2*maxDescribedViolations {
		t.Errorf("listed %d new violations, want %d:\n%s", n, 2*maxDescribedViolations, got)
	}
	if n := strings.Count(got, "(touched file must be clean)"); n != 2*maxDescribedViolations {
		t.Errorf("listed %d touched violations, want %d:\n%s", n, 2*maxDescribedViolations, got)
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

// attributionFixture is a baseline recorded at a commit, a scan that adds two findings of a check
// the recorder lacked in unchanged code and one finding in changed code, and what the current
// checks report at that commit.
func attributionFixture() (b *Baseline, current, atCommit []Infraction) {
	exitMsg := "sys.exit ends the process from library code"
	b = &Baseline{CommitSHA: "0123456789abcdef0123456789abcdef01234567", TotalInfractions: 0, Infractions: []Infraction{}}
	current = []Infraction{
		{RuleID: "HISS-07", FilePath: "hooks/guard.py", LineNumber: 50, Message: exitMsg, Fingerprint: "hooks/guard.py:50:HISS-07"},
		{RuleID: "HISS-07", FilePath: "hooks/guard.py", LineNumber: 58, Message: exitMsg, Fingerprint: "hooks/guard.py:58:HISS-07"},
		{RuleID: "HISS-02", FilePath: "hooks/new.py", LineNumber: 3, Message: "unbounded loop", Fingerprint: "hooks/new.py:3:HISS-02"},
	}
	// The commit holds the same two sys.exit calls, at other lines, and not the new loop.
	atCommit = []Infraction{
		{RuleID: "HISS-07", FilePath: "hooks/guard.py", LineNumber: 48, Message: exitMsg},
		{RuleID: "HISS-07", FilePath: "hooks/guard.py", LineNumber: 56, Message: exitMsg},
	}
	return b, current, atCommit
}

// Positive (#599): findings the current checks also report at the baseline's commit are
// attributed to a changed check and named with the remediation; the one in changed code is new.
func TestRatchetResultAttribute_Positive_ChangedCheckInUnchangedCode(t *testing.T) {
	b, current, atCommit := attributionFixture()
	res := EvaluateRatchet(b, current, nil)
	res.Attribute(b, current, atCommit, b.CommitSHA)
	want := []Attribution{AttributionCheckChanged, AttributionCheckChanged, AttributionIntroduced}
	if !slices.Equal(res.Attribution, want) {
		t.Fatalf("Attribution = %v, want %v", res.Attribution, want)
	}
	got := res.Summary()
	for _, line := range []string{
		"HISS invariant violations the baseline does not record (3 total infractions, 3 unbaselined (1 introduced, 2 from checks added or changed since the baseline), 0 in touched files):",
		"[HISS-07] hooks/guard.py:50 - sys.exit ends the process from library code (check added or changed since the baseline)",
		"[HISS-02] hooks/new.py:3 - unbounded loop (new)",
		"2 of them sit in code the current checks flag at 0123456789ab, the commit the baseline was recorded at",
		recordRemedy,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("Summary() missing %q:\n%s", line, got)
		}
	}
	if res.Passed {
		t.Error("an attribution must never pass the ratchet")
	}
}

// Negative (#599): a violation the commit does not hold is introduced, and one the baseline
// records at another line was known to the recorder, so it is never blamed on a changed check.
func TestRatchetResultAttribute_Negative_ChangedCodeAndMovedFinding(t *testing.T) {
	b, current, _ := attributionFixture()
	res := EvaluateRatchet(b, current, nil)
	res.Attribute(b, current, nil, b.CommitSHA)
	for i, got := range res.Attribution {
		if got != AttributionIntroduced {
			t.Errorf("Attribution[%d] = %q with nothing at the commit, want introduced", i, got)
		}
	}
	if got := res.Summary(); !strings.HasPrefix(got, "HISS invariant violations introduced (3 total infractions, 3 new unbaselined") {
		t.Errorf("fully introduced rejection = %q", got)
	}

	moved := Infraction{RuleID: "HISS-07", FilePath: "hooks/guard.py", LineNumber: 40, Message: current[0].Message, Fingerprint: "hooks/guard.py:40:HISS-07"}
	recorded := &Baseline{CommitSHA: b.CommitSHA, TotalInfractions: 1, Infractions: []Infraction{moved}}
	_, _, atCommit := attributionFixture()
	res = EvaluateRatchet(recorded, current, nil)
	res.Attribute(recorded, current, atCommit, recorded.CommitSHA)
	want := []Attribution{AttributionMoved, AttributionCheckChanged, AttributionIntroduced}
	if !slices.Equal(res.Attribution, want) || !slices.Equal(res.RecordedLine, []int{40, 0, 0}) {
		t.Fatalf("Attribution with a moved recorded finding = %v at %v, want %v at [40 0 0]", res.Attribution, res.RecordedLine, want)
	}
	got := res.Summary()
	for _, line := range []string{
		"(3 total infractions, 3 unbaselined (1 introduced, 1 from checks added or changed since the baseline, 1 recorded at another line), 0 in touched files):",
		"[HISS-07] hooks/guard.py:50 - sys.exit ends the process from library code (recorded in the baseline at line 40)",
		"[HISS-07] hooks/guard.py:58 - sys.exit ends the process from library code (check added or changed since the baseline)",
		"1 of them the baseline records at another line of the same file",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("Summary() missing %q:\n%s", line, got)
		}
	}
	if strings.Contains(got, "not traced") || strings.Contains(got, "(not in the baseline)") {
		t.Errorf("a moved finding is described as untraced although a commit was compared:\n%s", got)
	}
}

// movedFixture is the line drift of #29: the baseline records one finding at line 10, and lines
// added above it moved it to line 12. The debt did not change.
func movedFixture() (b *Baseline, current []Infraction) {
	msg := "sys.exit ends the process from library code"
	b = &Baseline{CommitSHA: "0123456789abcdef0123456789abcdef01234567", TotalInfractions: 1, Infractions: []Infraction{
		{RuleID: "HISS-07", FilePath: "a.py", LineNumber: 10, Message: msg, Fingerprint: "a.py:10:HISS-07"},
	}}
	current = []Infraction{{RuleID: "HISS-07", FilePath: "a.py", LineNumber: 12, Message: msg, Fingerprint: "a.py:12:HISS-07"}}
	return b, current
}

// Positive (#29, #599): a finding the baseline records at another line is tagged with that line,
// the header says the baseline records it elsewhere, and the remedy is the plain re-record: the
// count did not rise, so neither --allow-increase nor an untraced reason is named.
func TestRatchetResultAttribute_Positive_MovedFindingNeedsOnlyARerecord(t *testing.T) {
	b, current := movedFixture()
	res := EvaluateRatchet(b, current, nil)
	res.Attribute(b, current, []Infraction{b.Infractions[0]}, b.CommitSHA)
	if res.Passed || !slices.Equal(res.Attribution, []Attribution{AttributionMoved}) {
		t.Fatalf("passed=%v attribution=%v, want a rejection tagged moved", res.Passed, res.Attribution)
	}
	want := strings.Join([]string{
		"HISS invariant violations the baseline records at other lines (1 total infractions, 1 moved, 0 in touched files):",
		"  [HISS-07] a.py:12 - sys.exit ends the process from library code (recorded in the baseline at line 10)",
		"  1 of them the baseline records at another line of the same file: lines above them were added or removed, " +
			"and the debt did not change; re-record the baseline with 'praetorctl baseline --record', which needs no --allow-increase for them",
	}, "\n")
	if got := res.Summary(); got != want {
		t.Errorf("Summary() =\n%s\nwant\n%s", got, want)
	}
}

// Boundary (#29): moving needs no commit to tell. A baseline without commit_sha still tags the
// moved finding and gives every other one the reason, and an untraced attribution with nothing
// moved leaves Attribution nil; a nil result or baseline is left alone.
func TestRatchetResultAttributeUntraced_Boundary_MovedNeedsNoCommit(t *testing.T) {
	b, current := movedFixture()
	b.CommitSHA = ""
	added := Infraction{RuleID: "HISS-02", FilePath: "b.py", LineNumber: 3, Message: "unbounded loop", Fingerprint: "b.py:3:HISS-02"}
	current = append(current, added)
	note := "the baseline records no commit to compare against"
	res := EvaluateRatchet(b, current, nil)
	res.AttributeUntraced(b, current, note)
	if !slices.Equal(res.Attribution, []Attribution{AttributionMoved, AttributionNone}) || res.AttributionNote != note || res.AttributionCommit != "" {
		t.Fatalf("attribution %v note %q commit %q, want [moved none] with the note", res.Attribution, res.AttributionNote, res.AttributionCommit)
	}
	got := res.Summary()
	for _, line := range []string{
		"(2 total infractions, 2 unbaselined (1 recorded at another line, 1 unattributed), 0 in touched files):",
		"(recorded in the baseline at line 10)",
		"[HISS-02] b.py:3 - unbounded loop (not in the baseline)",
		"1 of them were not traced to a code change (" + note + ")",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("Summary() missing %q:\n%s", line, got)
		}
	}

	res = EvaluateRatchet(b, []Infraction{added}, nil)
	res.AttributeUntraced(b, []Infraction{added}, note)
	if res.Attribution != nil || res.RecordedLine != nil || res.AttributionNote != note {
		t.Errorf("nothing moved: attribution %v lines %v note %q, want nil, nil and the note", res.Attribution, res.RecordedLine, res.AttributionNote)
	}
	res.AttributeUntraced(nil, current, "other")
	var nilResult *RatchetResult
	nilResult.AttributeUntraced(b, current, note)
	if res.Attribution != nil || res.AttributionNote != "other" {
		t.Errorf("a nil baseline tagged findings or dropped the note: %v %q", res.Attribution, res.AttributionNote)
	}
}

// Boundary (#599): without an attribution, as for a baseline that records no commit, every new
// violation is described as not in the baseline with the reason and the remediation, never as
// introduced; a nil baseline or result leaves the result as it was.
func TestRatchetResultAttribute_Boundary_UnattributedIsNeutral(t *testing.T) {
	b, current, _ := attributionFixture()
	res := EvaluateRatchet(b, current, nil)
	res.AttributionNote = "the baseline records no commit to compare against"
	got := res.Summary()
	if strings.Contains(got, "introduced") || strings.Contains(got, "(new)") {
		t.Errorf("an unattributed rejection claims the change introduced it:\n%s", got)
	}
	for _, line := range []string{
		"HISS invariant violations the baseline does not record (3 total infractions, 3 unbaselined, 0 in touched files):",
		"(not in the baseline)",
		"3 of them were not traced to a code change (the baseline records no commit to compare against)",
		recordRemedy,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("Summary() missing %q:\n%s", line, got)
		}
	}

	res.Attribute(nil, current, nil, "")
	var nilResult *RatchetResult
	nilResult.Attribute(b, current, nil, "")
	if res.Attribution != nil {
		t.Errorf("Attribute with a nil baseline changed the result: %v", res.Attribution)
	}
}
