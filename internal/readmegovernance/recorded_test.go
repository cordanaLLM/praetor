package readmegovernance

import (
	"errors"
	"strings"
	"testing"
)

// Positive: every state shape Reconcile renders reads back to the same state, including a
// README whose custom HISS badge suppresses the managed one, a HISS badge linked into the
// repository without the documentation gate, and a CRLF README.
func TestRecordedStatePositiveReadsBackEveryRenderedShape(t *testing.T) {
	docs := State{BaselineKnown: true, LegacyDebtCount: 7, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}
	cases := map[string]struct {
		input string
		state State
	}{
		"baseline pending": {"# Demo\n\nHuman text.\n", State{}},
		"clean baseline":   {"# Demo\n", State{BaselineKnown: true}},
		"one infraction":   {"# Demo\n", State{BaselineKnown: true, LegacyDebtCount: 1}},
		"linked badge":     {"# Demo\n", State{BaselineKnown: true, RepositoryOwner: "acme", RepositoryName: "widgets"}},
		"documentation":    {"# Demo\n\nHuman text.\n", docs},
		"custom badge":     {"# Demo\n\n[![HISS policy](https://img.shields.io/badge/Custom-HISS-blue)](policy.md)\n", docs},
		"crlf":             {"# Demo\r\n\r\nHuman text.\r\n", docs},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rendered, _, err := Reconcile(tc.input, tc.state)
			if err != nil {
				t.Fatal(err)
			}
			got, managed, err := RecordedState(rendered, "acme", "widgets")
			if err != nil || !managed {
				t.Fatalf("read back: managed=%v err=%v", managed, err)
			}
			if got != tc.state {
				t.Fatalf("read back %+v, rendered %+v", got, tc.state)
			}
		})
	}
}

// Positive: the read-back state with another identity renders the block for that repository,
// the AGENTS.md link included with or without the documentation gate, and leaves every byte
// outside the block alone.
func TestRecordedStatePositiveRebindsThroughReconcile(t *testing.T) {
	for _, docs := range []bool{true, false} {
		source := State{BaselineKnown: true, LegacyDebtCount: 2, DocumentationEnabled: docs, RepositoryOwner: "acme", RepositoryName: "widgets"}
		rendered, _, err := Reconcile("# Demo\n\nHuman text.\n", source)
		if err != nil {
			t.Fatal(err)
		}
		state, _, err := RecordedState(rendered, "acme", "widgets")
		if err != nil {
			t.Fatal(err)
		}
		state.RepositoryOwner = "example"
		rebound, changed, err := Reconcile(rendered, state)
		if err != nil || !changed {
			t.Fatalf("rebind: changed=%v err=%v", changed, err)
		}
		if want := strings.ReplaceAll(rendered, "github.com/acme/widgets/", "github.com/example/widgets/"); rebound != want {
			t.Fatalf("rebind changed more than the badge identity:\n%s", rebound)
		}
		if !strings.Contains(rebound, "https://github.com/example/widgets/blob/HEAD/AGENTS.md\n") {
			t.Fatalf("rebound block does not link the fork's AGENTS.md:\n%s", rebound)
		}
		fork := source
		fork.RepositoryOwner = "example"
		if err := Verify(rebound, fork); err != nil {
			t.Fatalf("rebound block fails the fork's own verification: %v", err)
		}
	}
}

// Negative: a hand-edited block, a badge linking another repository, a block without its debt
// row and malformed markers never yield a state.
func TestRecordedStateNegativeRefusesUnrenderedBlocks(t *testing.T) {
	state := State{BaselineKnown: true, LegacyDebtCount: 3, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}
	rendered, _, err := Reconcile("# Demo\n", state)
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]string{
		"hand-edited count": strings.Replace(rendered, "3 recorded infractions", "9 recorded infractions", 1),
		"other repository":  strings.ReplaceAll(rendered, "github.com/acme/widgets/", "github.com/other/widgets/"),
		"no debt row":       strings.Replace(rendered, debtRecordedLine+"\n3 recorded infractions"+debtRecordedSuffix+"\n", "", 1),
		"unreadable count":  strings.Replace(rendered, "3 recorded infractions", "three recorded infractions", 1),
	}
	for name, input := range stale {
		t.Run(name, func(t *testing.T) {
			if _, managed, err := RecordedState(input, "acme", "widgets"); !errors.Is(err, ErrStale) || managed {
				t.Fatalf("managed=%v err=%v, want %v", managed, err, ErrStale)
			}
		})
	}
	malformed := "# Demo\n\n" + Start + "\nunterminated\n"
	if _, _, err := RecordedState(malformed, "acme", "widgets"); err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed markers: %v", err)
	}
	if _, _, err := RecordedState("# Demo\r\n\nmixed\n", "acme", "widgets"); err == nil {
		t.Fatal("inconsistent line endings accepted")
	}
}

// Boundary: a README without a managed block reports no state and no error.
func TestRecordedStateBoundaryReportsAbsentBlock(t *testing.T) {
	for _, input := range []string{"", "# Demo\n\nHuman text.\n"} {
		if state, managed, err := RecordedState(input, "acme", "widgets"); err != nil || managed || state != (State{}) {
			t.Fatalf("%q: state=%+v managed=%v err=%v", input, state, managed, err)
		}
	}
}

// Positive: two READMEs that differ only inside the managed block compare equal without its
// body; the markers and every outside line stay.
func TestWithoutBlockBodyPositiveIgnoresOnlyTheBlockBody(t *testing.T) {
	a := "# Demo\n\n" + Start + "\nold wording\n" + End + "\n\nHuman text.\n"
	b := "# Demo\n\n" + Start + "\nnew wording\nsecond line\n" + End + "\n\nHuman text.\n"
	strippedA, err := WithoutBlockBody(a)
	if err != nil {
		t.Fatal(err)
	}
	strippedB, err := WithoutBlockBody(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Demo\n\n" + Start + "\n" + End + "\n\nHuman text.\n"; strippedA != want || strippedB != want {
		t.Fatalf("stripped:\n%q\n%q", strippedA, strippedB)
	}
	crlf, err := WithoutBlockBody(strings.ReplaceAll(a, "\n", "\r\n"))
	if err != nil || crlf != strings.ReplaceAll(strippedA, "\n", "\r\n") {
		t.Fatalf("CRLF strip: %q err=%v", crlf, err)
	}
}

// Negative: unbalanced or duplicated markers are an error, never a partial strip.
func TestWithoutBlockBodyNegativeRejectsMalformedMarkers(t *testing.T) {
	for name, input := range map[string]string{
		"unterminated": "# Demo\n" + Start + "\nbody\n",
		"end first":    "# Demo\n" + End + "\n" + Start + "\n",
		"duplicated":   Start + "\n" + End + "\n" + Start + "\n" + End + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := WithoutBlockBody(input); err == nil || !strings.Contains(err.Error(), "README governance markers") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

// Boundary: content without a managed block, or with an empty one, is returned unchanged.
func TestWithoutBlockBodyBoundaryKeepsContentWithoutBody(t *testing.T) {
	for _, input := range []string{"", "# Demo\n\nHuman text.\n", Start + "\n" + End + "\n"} {
		if got, err := WithoutBlockBody(input); err != nil || got != input {
			t.Fatalf("%q: got %q err=%v", input, got, err)
		}
	}
}
