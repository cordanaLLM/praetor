// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// readRepoFile returns the text of rel under repo.
func readRepoFile(t *testing.T, repo, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// stricterComplexity is the #321 reproduction's override: every HISS-04 limit below its default.
var stricterComplexity = hiss.ComplexityLimits{MaxCyclomatic: 8, MaxCognitive: 12, MaxStatements: 40}

// stricterRow is the HISS-04 row a Go repository under stricterComplexity and a 45-line function
// length reads.
const stricterRow = "HISS-04: McCabe cyclomatic <= 8, cognitive <= 12, statements <= 40; func LOC <= 45"

// stricterFacts are the facts of a Go repository whose policy resolves the stricter override.
func stricterFacts() hisscatalog.Facts {
	return hisscatalog.Facts{Languages: hisscatalog.LanguageGo, MaxFuncLOC: 45, Complexity: stricterComplexity}
}

// TestSynthesizeHarness_Positive_ComplexityOverrideReachesRules (#321): a stricter cyclomatic,
// cognitive and statement override reaches harness.json's HISS-04 row and the rules.md
// rendering, not the function length alone.
func TestSynthesizeHarness_Positive_ComplexityOverrideReachesRules(t *testing.T) {
	repo := identifiedRepo(t)
	h, _, err := SynthesizeHarness(t.Context(), repo, stricterFacts())
	if err != nil {
		t.Fatal(err)
	}
	if h.Invariants[2] != stricterRow {
		t.Fatalf("HISS-04 = %q, want %q", h.Invariants[2], stricterRow)
	}
	if err := WriteHarness(h, repo); err != nil {
		t.Fatal(err)
	}
	rules := readRepoFile(t, repo, ".paperclip/rules.md")
	if !strings.Contains(strings.ReplaceAll(rules, "\n  ", " "), "- "+stricterRow+"\n") {
		t.Fatalf("rules.md lacks the override row:\n%s", rules)
	}
}

// TestSynthesizeHarness_Negative_UnresolvedComplexityStatesDefaults (#321): without a resolved
// policy the row states the HISS-04 defaults, byte for byte the row every earlier release wrote,
// and never a format verb.
func TestSynthesizeHarness_Negative_UnresolvedComplexityStatesDefaults(t *testing.T) {
	text := invariantsForFacts(t, hisscatalog.Facts{Languages: hisscatalog.LanguageGo, MaxFuncLOC: 45})
	if !strings.Contains(text, "HISS-04: McCabe cyclomatic <= 10, cognitive <= 15, statements <= 50; func LOC <= 45") ||
		strings.Contains(text, "%") {
		t.Fatalf("unresolved complexity does not state the defaults:\n%s", text)
	}
}

// TestPriorGenerated_Positive_ComplexityResolvesIsEarlierOutput (#321): a harness written while
// the complexity limits were unresolved, or by a release that stated the defaults whatever the
// policy said, is unmodified earlier output once the stricter override resolves, so a plain adopt
// refreshes it and the audit names it stale rather than operator-owned.
func TestPriorGenerated_Positive_ComplexityResolvesIsEarlierOutput(t *testing.T) {
	unresolved := hisscatalog.Facts{Languages: hisscatalog.LanguageGo, MaxFuncLOC: 45}
	repo, current := writtenWidget(t, "", "", unresolved, stricterFacts())
	state, err := PriorGenerated(context.Background(), repo, current)
	if err != nil || !state.Generated || !state.Rules {
		t.Fatalf("harness stating the defaults is not earlier output of the override: prior=%+v err=%v", state, err)
	}
	// The override's own harness stays earlier output after another fact moves, here the key.
	repo, current = writtenWidget(t, "", pinnedReceipt, stricterFacts(), stricterFacts())
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || !state.Generated {
		t.Fatalf("harness stating the override is not earlier output after pinning the key: prior=%+v err=%v", state, err)
	}
}

// TestPriorGenerated_Negative_EditedComplexityStaysOwned (#321): a hand edit of one complexity
// limit, to a value no synthesis for the repository states, keeps the harness operator-owned,
// as an edited function length does (limitFacts).
func TestPriorGenerated_Negative_EditedComplexityStaysOwned(t *testing.T) {
	repo := t.TempDir()
	written := synthesizeWidget(t, repo, "", stricterFacts())
	written.Invariants[2] = strings.Replace(written.Invariants[2], "cognitive <= 12", "cognitive <= 11", 1)
	if err := WriteHarness(written, repo); err != nil {
		t.Fatal(err)
	}
	current := synthesizeWidget(t, repo, pinnedReceipt, stricterFacts())
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
		t.Fatalf("edited complexity limit read as earlier output: prior=%+v err=%v", state, err)
	}
}

// TestComplexityFacts_Boundary states which complexity sets the refresh key accepts. The
// unresolved set always; a stated set only when it differs from the defaults, once; and the
// statements a current synthesis states are read back from its own invariants, once each, and
// from no more than maxHarnessValues of them.
func TestComplexityFacts_Boundary(t *testing.T) {
	defaults := hiss.ComplexityLimits{}.WithDefaults()
	if got := complexityFacts([]hiss.ComplexityLimits{defaults}); len(got) != 1 || got[0] != (hiss.ComplexityLimits{}) {
		t.Fatalf("complexityFacts(defaults) = %+v, want the unresolved set alone", got)
	}
	got := complexityFacts([]hiss.ComplexityLimits{stricterComplexity, stricterComplexity})
	if len(got) != 2 || got[1] != stricterComplexity {
		t.Fatalf("complexityFacts(stricter twice) = %+v, want unresolved and stricter once", got)
	}
	stated := statedComplexities([]string{stricterRow, "HISS-04: McCabe cyclomatic <= 10, cognitive <= 15, statements <= 50", stricterRow})
	if len(stated) != 2 || stated[0] != stricterComplexity || stated[1] != defaults {
		t.Fatalf("statedComplexities = %+v, want [stricter defaults]", stated)
	}
	if got := statedComplexities([]string{"McCabe cyclomatic <= 0, cognitive <= 12, statements <= 40"}); len(got) != 0 {
		t.Fatalf("statedComplexities read a zero limit: %+v", got)
	}
	long := make([]string, maxHarnessValues+1)
	long[maxHarnessValues] = stricterRow
	if got := statedComplexities(long); len(got) != 0 {
		t.Fatalf("statedComplexities read past maxHarnessValues: %+v", got)
	}
	statements := policyFacts(statedPolicy{funcLOCs: []int{45}, complexity: []hiss.ComplexityLimits{stricterComplexity}})
	if len(statements) != len(limitFacts([]int{45}))*2 {
		t.Fatalf("policyFacts = %d statements, want every length statement under both complexity sets", len(statements))
	}
}
