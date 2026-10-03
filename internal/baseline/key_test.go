package baseline

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// anchored builds the findings of one scan and keys them the way a scan is keyed.
func anchored(findings ...Infraction) []Infraction {
	out := slices.Clone(findings)
	AssignFingerprints(out)
	return out
}

// loop is an unbounded-loop finding of file at line, held by anchor.
func loop(file string, line int, anchor string) Infraction {
	return Infraction{RuleID: "HISS-02", FilePath: file, LineNumber: line, Anchor: anchor, Message: "Unbounded loop"}
}

func fingerprints(infractions []Infraction) []string {
	out := make([]string, 0, len(infractions))
	for i := 0; i < len(infractions); i++ {
		out = append(out, infractions[i].Fingerprint)
	}
	return out
}

// Positive: an anchored finding is keyed by file, rule, anchor and rank. Negative: one without
// an anchor is keyed by its line, the line-keyed form. Boundary: the rank follows line order
// whatever the scan order, a path is keyed with forward slashes, and an empty scan is left.
func TestAssignFingerprints_3D(t *testing.T) {
	got := fingerprints(anchored(
		loop("a.go", 40, "fn:spin"), loop("a.go", 12, "fn:spin"), loop("a.go", 90, "fn:T.Run"),
		loop(`dir\b.go`, 3, "text:0123456789ab"), loop("a.go", 7, "")))
	want := []string{"a.go:HISS-02:fn:spin#2", "a.go:HISS-02:fn:spin#1", "a.go:HISS-02:fn:T.Run#1",
		"dir/b.go:HISS-02:text:0123456789ab#1", "a.go:7:HISS-02"}
	if !slices.Equal(got, want) {
		t.Fatalf("fingerprints %v, want %v", got, want)
	}

	sameLine := fingerprints(anchored(loop("a.go", 5, "fn:f"), loop("a.go", 5, "fn:f")))
	if !slices.Equal(sameLine, []string{"a.go:HISS-02:fn:f#1", "a.go:HISS-02:fn:f#2"}) {
		t.Fatalf("two findings on one line must rank in scan order: %v", sameLine)
	}
	AssignFingerprints(nil)
}

// Positive (#29): a function moved by lines inserted above it still matches its entry, in an
// untouched and in a judged file alike, and nothing is stale.
func TestEvaluateRatchet_Positive_MovedFunctionStaysBaselined(t *testing.T) {
	recorded := anchored(loop("a.go", 12, "fn:spin"), loop("a.go", 40, "fn:spin"), loop("b.py", 3, "text:aaaaaaaaaaaa"))
	b := &Baseline{Version: 1, Infractions: recorded, TotalInfractions: len(recorded)}
	shifted := anchored(loop("a.go", 19, "fn:spin"), loop("a.go", 47, "fn:spin"), loop("b.py", 9, "text:aaaaaaaaaaaa"))

	res := EvaluateRatchet(b, shifted, nil)
	if !res.Passed || len(res.NewViolations) != 0 || res.Stale != 0 {
		t.Fatalf("moved findings must stay baselined: %+v", res)
	}
	delta := EvaluateRatchetWithOptions(b, shifted, []string{"a.go"}, RatchetOptions{DebtDelta: true})
	if !delta.Passed {
		t.Fatalf("a touched file whose findings only moved carries no new debt: %s", delta.Summary())
	}
}

// Negative: a new function with the same violation is a new finding, and so is the finding of
// a renamed function. The ratchet's guarantees hold: the touched file is still refused, and the
// total cannot rise without the deliberate flag.
func TestEvaluateRatchet_Negative_NewAndRenamedFunctionsAreNew(t *testing.T) {
	recorded := anchored(loop("a.go", 12, "fn:spin"))
	b := &Baseline{Version: 1, Infractions: recorded, TotalInfractions: 1}

	added := anchored(loop("a.go", 4, "fn:fresh"), loop("a.go", 20, "fn:spin"))
	res := EvaluateRatchet(b, added, nil)
	if res.Passed || len(res.NewViolations) != 1 || res.NewViolations[0].Anchor != "fn:fresh" {
		t.Fatalf("a new function with the same violation must be the one new finding: %+v", res)
	}

	renamed := anchored(loop("a.go", 12, "fn:turn"))
	res = EvaluateRatchet(b, renamed, nil)
	if res.Passed || len(res.NewViolations) != 1 || res.CurrentCount != res.PreviousCount {
		t.Fatalf("a renamed function's finding must be new at an unchanged total: %+v", res)
	}
	// The attribution still explains it without a commit: the baseline records the same finding
	// under the old name, so the plain re-record is the remedy (describe.go).
	res.AttributeUntraced(b, renamed, "no commit")
	if !slices.Equal(res.Attribution, []Attribution{AttributionMoved}) || !strings.Contains(res.Summary(), "under another key of the same file") {
		t.Fatalf("the renamed function's finding must be attributed as recorded under another key: %v\n%s", res.Attribution, res.Summary())
	}

	touched := EvaluateRatchet(b, anchored(loop("a.go", 30, "fn:spin")), []string{"a.go"})
	if touched.Passed || len(touched.TouchedCleanViolations) != 1 || !touched.TouchedBaselined[0] {
		t.Fatalf("a touched file is still refused, its moved finding marked baselined: %+v", touched)
	}

	if _, err := Record(b, added, RecordOptions{}); !errors.Is(err, ErrDebtIncrease) {
		t.Fatalf("recording one more finding must be refused, got %v", err)
	}
}

// Boundary: a second finding of the rule in the baselined function is new, one of two leaving
// is stale, and a renamed file carries new keys while its old entries go stale.
func TestEvaluateRatchet_Boundary_OrdinalsAndFileRename(t *testing.T) {
	recorded := anchored(loop("a.go", 12, "fn:spin"), loop("a.go", 40, "fn:spin"))
	b := &Baseline{Version: 1, Infractions: recorded, TotalInfractions: 2}

	third := anchored(loop("a.go", 5, "fn:spin"), loop("a.go", 12, "fn:spin"), loop("a.go", 40, "fn:spin"))
	res := EvaluateRatchet(b, third, nil)
	if res.Passed || len(res.NewViolations) != 1 || !strings.HasSuffix(res.NewViolations[0].Fingerprint, "#3") {
		t.Fatalf("a third finding in the function must be the one new key: %+v", res.NewViolations)
	}

	res = EvaluateRatchet(b, anchored(loop("a.go", 40, "fn:spin")), nil)
	if !res.Passed || res.Stale != 1 {
		t.Fatalf("one finding leaving the function passes with one stale entry: %+v", res)
	}

	res = EvaluateRatchet(b, anchored(loop("z.go", 12, "fn:spin"), loop("z.go", 40, "fn:spin")), nil)
	if res.Passed || len(res.NewViolations) != 2 || res.Stale != 2 || res.CurrentCount != res.PreviousCount {
		t.Fatalf("a renamed file must carry new keys at an unchanged total: %+v", res)
	}
}

// lineKeyedBaseline is a baseline as recorded before entries carried an anchor.
func lineKeyedBaseline() *Baseline {
	return &Baseline{Version: 1, CommitSHA: "abc", TotalInfractions: 2, Infractions: []Infraction{
		{RuleID: "HISS-02", FilePath: "a.go", LineNumber: 12, Message: "Unbounded loop", Fingerprint: "a.go:12:HISS-02"},
		{RuleID: "HISS-02", FilePath: "a.go", LineNumber: 40, Message: "Unbounded loop", Fingerprint: "a.go:40:HISS-02"},
	}}
}

// Positive: a baseline recorded in the line-keyed form still verifies against an anchored scan
// of the unchanged tree, in a judged file too, and re-recording it writes the anchored form at
// the same count without the increase flag.
func TestLineKeyedBaseline_Positive_VerifiesAndMigrates(t *testing.T) {
	b := lineKeyedBaseline()
	current := anchored(loop("a.go", 12, "fn:spin"), loop("a.go", 40, "fn:spin"))

	res := EvaluateRatchet(b, current, nil)
	if !res.Passed || res.Stale != 0 {
		t.Fatalf("a line-keyed baseline must verify an unchanged tree: %+v", res)
	}
	touched := EvaluateRatchet(b, current, []string{"a.go"})
	if marks := touched.TouchedBaselined; len(marks) != 2 || !marks[0] || !marks[1] {
		t.Fatalf("line-keyed entries must account for the touched findings: %v", marks)
	}

	next, err := Record(b, current, RecordOptions{CommitSHA: "def"})
	if err != nil || next.Count() != b.Count() || next.IncreaseRationale != "" {
		t.Fatalf("migration must keep the count without an increase: %+v, %v", next, err)
	}
	if b.SameDebt(next) || next.Infractions[1].Fingerprint != "a.go:HISS-02:fn:spin#2" || next.Infractions[1].Anchor != "fn:spin" {
		t.Fatalf("re-recording must write the anchored form: %+v", next.Infractions)
	}
	if again := EvaluateRatchet(next, anchored(loop("a.go", 90, "fn:spin"), loop("a.go", 95, "fn:spin")), nil); !again.Passed {
		t.Fatalf("the migrated baseline must survive a shift: %s", again.Summary())
	}
}

// Negative: a line-keyed entry is matched on its line and nothing else, so a shifted finding is
// still refused until the baseline is re-recorded, and the attribution says it only moved.
// Boundary: one entry of each form in one baseline, each matched in its own way; an anchored
// entry is never matched on its line.
func TestLineKeyedBaseline_NegativeAndBoundary(t *testing.T) {
	b := lineKeyedBaseline()
	shifted := anchored(loop("a.go", 15, "fn:spin"), loop("a.go", 43, "fn:spin"))
	res := EvaluateRatchet(b, shifted, nil)
	if res.Passed || len(res.NewViolations) != 2 {
		t.Fatalf("a line-keyed entry must not match a shifted finding: %+v", res)
	}
	res.AttributeUntraced(b, shifted, "no commit")
	if !slices.Equal(res.Attribution, []Attribution{AttributionMoved, AttributionMoved}) || res.RecordedLine[0] != 12 {
		t.Fatalf("the shifted findings must be attributed as moved from their recorded lines: %+v %v", res.Attribution, res.RecordedLine)
	}
	if _, err := Record(b, shifted, RecordOptions{}); err != nil {
		t.Fatalf("re-recording the moved findings needs no increase: %v", err)
	}

	mixed := &Baseline{Version: 1, TotalInfractions: 2, Infractions: append(
		anchored(loop("a.go", 12, "fn:spin")), lineKeyedBaseline().Infractions[1])}
	if res := EvaluateRatchet(mixed, anchored(loop("a.go", 70, "fn:spin"), loop("a.go", 40, "fn:late")), nil); !res.Passed {
		t.Fatalf("each entry must be matched in its own form: %s", res.Summary())
	}
	onLine := anchored(loop("a.go", 12, "fn:other"))
	if res := EvaluateRatchet(&Baseline{Version: 1, TotalInfractions: 1, Infractions: anchored(loop("a.go", 12, "fn:spin"))}, onLine, nil); res.Passed {
		t.Fatal("an anchored entry must not be matched on its line")
	}
}

// The serialized entry: the anchor is written with the anchored form and left out of the
// line-keyed one, so a baseline in the old form loads and saves without gaining a field.
func TestInfractionJSON_AnchorMarksTheForm(t *testing.T) {
	entry := anchored(loop("a.go", 12, "fn:spin"))[0]
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"anchor":"fn:spin","fingerprint":"a.go:HISS-02:fn:spin#1"`) {
		t.Fatalf("anchored entry JSON: %s", data)
	}
	old := lineKeyedBaseline().Infractions[0]
	data, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var back Infraction
	if strings.Contains(string(data), "anchor") || json.Unmarshal(data, &back) != nil || back != old {
		t.Fatalf("line-keyed entry must round-trip without an anchor: %s", data)
	}
}
