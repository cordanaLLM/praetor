// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// widgetManifest declares acme/widget; pinnedReceipt pins a well-formed receipt key.
const (
	widgetManifest = "repository:\n  owner: acme\n  name: widget\n  forge: forgejo\n"
	pinnedReceipt  = "receipt:\n  public_key: \"" + "abababababababababababababababababababababababababababababababab" + "\"\n"
)

// synthesizeWidget rewrites the manifest of repo with receiptSection and synthesizes its harness
// for facts, as one adoption run does.
func synthesizeWidget(t *testing.T, repo, receiptSection string, facts hisscatalog.Facts) *Harness {
	t.Helper()
	writeRepoFile(t, repo, ".standards.yaml", widgetManifest+receiptSection)
	h, err := SynthesizeHarness(context.Background(), repo, facts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// writtenWidget writes this release's harness for receiptSection and facts, then returns the
// repository after its facts changed to the next ones, with the harness synthesized there.
func writtenWidget(t *testing.T, before, after string, from, to hisscatalog.Facts) (string, *Harness) {
	t.Helper()
	repo := t.TempDir()
	if err := WriteHarness(synthesizeWidget(t, repo, before, from), repo); err != nil {
		t.Fatal(err)
	}
	return repo, synthesizeWidget(t, repo, after, to)
}

// languageFacts are the facts of a repository carrying languages and nothing else known.
func languageFacts(languages hisscatalog.Language) hisscatalog.Facts {
	return hisscatalog.Facts{Languages: languages}
}

// TestPriorGeneratedRecognisesThisReleaseAfterFactsChange: Positive. The unpinned receipt row
// tells the operator to pin a key; once pinned, the harness adoption wrote is still unmodified
// output, so plain adopt refreshes it to the row that requires receipts. The same holds when the
// repository gains a language or declares an exception, and in reverse, and when its policy
// resolves the function length the harness stated as the unresolved audit ceiling, below the
// ceiling or at it.
func TestPriorGeneratedRecognisesThisReleaseAfterFactsChange(t *testing.T) {
	cFacts := languageFacts(hisscatalog.LanguageC)
	cException := hisscatalog.Facts{Languages: hisscatalog.LanguageC, Exceptions: hisscatalog.ExceptionCleanupGoto}
	cases := map[string]struct {
		before, after string
		from, to      hisscatalog.Facts
		want          string
	}{
		"pin key after adopt":  {"", pinnedReceipt, languageFacts(hisscatalog.LanguageGo), languageFacts(hisscatalog.LanguageGo), pinnedReceiptRow},
		"unpin key":            {pinnedReceipt, "", unknownFacts, unknownFacts, unpinnedReceiptPrefix},
		"Go gains Rust":        {"", "", languageFacts(hisscatalog.LanguageGo), languageFacts(hisscatalog.LanguageGo | hisscatalog.LanguageRust), "Rust: zero `.unwrap()`"},
		"Rust drops to none":   {"", "", languageFacts(hisscatalog.LanguageRust), unknownFacts, "Go: I/O takes `context.Context` deadline"},
		"pin key and add lang": {"", pinnedReceipt, unknownFacts, cFacts, pinnedReceiptRow},
		"declare exception":    {"", "", cFacts, cException, "(declared exception)"},
		"withdraw exception":   {"", "", cException, cFacts, "C/C++: zero `goto`"},
		"policy resolves":      {"", "", cFacts, hisscatalog.Facts{Languages: hisscatalog.LanguageC, MaxFuncLOC: 50}, "func LOC <= 50"},
		"policy resolves at ceiling": {"", "", cFacts,
			hisscatalog.Facts{Languages: hisscatalog.LanguageC, MaxFuncLOC: config.AuditMaxFuncLOC}, "(audit ceiling)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo, current := writtenWidget(t, tc.before, tc.after, tc.from, tc.to)
			state, err := PriorGenerated(context.Background(), repo, current)
			if err != nil || !state.Generated || !state.Rules {
				t.Fatalf("harness of this release under earlier facts is not earlier output: prior=%+v err=%v", state, err)
			}
			if text := strings.Join(append(current.OperatingContract, current.Invariants...), "\n"); !strings.Contains(text, tc.want) {
				t.Fatalf("refreshed harness lacks %q:\n%s", tc.want, text)
			}
		})
	}
}

// TestPriorGeneratedKeepsEditedFactRows: Negative. Recognition stays byte for byte, so an edit
// to the receipt row or to one invariant, or a rules.md rendered under other facts than its
// harness.json, keeps the harness operator-owned after the facts change. A function length is
// no exception: an edit to the number alone, in any form HISS-04 renders, stays operator-owned,
// since the refresh key takes no number from the harness on disk (limitFacts).
func TestPriorGeneratedKeepsEditedFactRows(t *testing.T) {
	edits := map[string]func(h *Harness) *Harness{
		"edited receipt row": func(h *Harness) *Harness {
			h.OperatingContract[3] = strings.Replace(h.OperatingContract[3], "attach no receipt", "attach a receipt", 1)
			return h
		},
		"edited invariant": func(h *Harness) *Harness {
			h.Invariants[1] += "; local exception"
			return h
		},
		"edited function length": func(h *Harness) *Harness {
			h.Invariants[2] = strings.Replace(h.Invariants[2], "(audit ceiling; stricter repository policy wins)", "lines", 1)
			return h
		},
		"edited number, ceiling suffix kept": func(h *Harness) *Harness {
			return editedFuncLOC(t, h, "60 (audit ceiling; stricter", "45 (audit ceiling; stricter")
		},
		"edited number, at-ceiling suffix": func(h *Harness) *Harness {
			return editedFuncLOC(t, h, "60 (audit ceiling; stricter repository policy wins)", "45 (audit ceiling)")
		},
		"edited number, plain": func(h *Harness) *Harness {
			return editedFuncLOC(t, h, "60 (audit ceiling; stricter repository policy wins)", "45")
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if err := WriteHarness(edit(synthesizeWidget(t, repo, "", languageFacts(hisscatalog.LanguageGo))), repo); err != nil {
				t.Fatal(err)
			}
			current := synthesizeWidget(t, repo, pinnedReceipt, languageFacts(hisscatalog.LanguageGo|hisscatalog.LanguageRust))
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
				t.Fatalf("%s read as unmodified earlier output: prior=%+v err=%v", name, state, err)
			}
		})
	}
	repo, current := writtenWidget(t, "", pinnedReceipt, languageFacts(hisscatalog.LanguageGo), languageFacts(hisscatalog.LanguageGo))
	writeRepoFile(t, repo, ".paperclip/rules.md", renderRules(current))
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
		t.Fatalf("rules.md of other facts beside harness.json read as earlier output: prior=%+v err=%v", state, err)
	}
}

// editedFuncLOC replaces from with to in h's HISS-04 invariant, as an operator's hand edit of
// the function length would.
func editedFuncLOC(t *testing.T, h *Harness, from, to string) *Harness {
	t.Helper()
	edited := strings.Replace(h.Invariants[2], "func LOC <= "+from, "func LOC <= "+to, 1)
	if edited == h.Invariants[2] {
		t.Fatalf("HISS-04 invariant %q states no %q", h.Invariants[2], from)
	}
	h.Invariants[2] = edited
	return h
}

// TestPriorGeneratedKeepsHarnessAfterResolvedLimitMoves: Negative. Once a policy has resolved
// the function length, a harness stating that plain number is operator-owned after the limit
// moves: to another number, up to the ceiling, or back to the unresolved ceiling. Only the
// number the current synthesis states is accepted as a plain number, since any other one is
// indistinguishable from a hand edit, so the harness stays operator-owned, under --force too;
// deleting it and re-running adopt regenerates it. Boundary: the same plain number with only
// the receipt row changed is still earlier output.
func TestPriorGeneratedKeepsHarnessAfterResolvedLimitMoves(t *testing.T) {
	resolved := func(limit int) hisscatalog.Facts {
		return hisscatalog.Facts{Languages: hisscatalog.LanguageGo, MaxFuncLOC: limit}
	}
	for name, to := range map[string]hisscatalog.Facts{
		"50 to 45":           resolved(45),
		"50 to ceiling":      resolved(config.AuditMaxFuncLOC),
		"50 to unresolved":   languageFacts(hisscatalog.LanguageGo),
		"50 to one below 50": resolved(49),
		"50 to one above 50": resolved(51),
	} {
		t.Run(name, func(t *testing.T) {
			repo, current := writtenWidget(t, "", "", resolved(50), to)
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
				t.Fatalf("harness stating 50 read as earlier output of a synthesis stating %v: prior=%+v err=%v",
					current.Invariants[2], state, err)
			}
		})
	}
	repo, current := writtenWidget(t, "", pinnedReceipt, resolved(50), resolved(50))
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || !state.Generated {
		t.Fatalf("harness stating the current limit is not earlier output after pinning the key: prior=%+v err=%v", state, err)
	}
}

// TestPriorGeneratedRecognisesThisReleaseUnderEveryFactCombination: Boundary. Every value the
// synthesis distinguishes (every released push row pair and receipt row times every language set up to AllLanguages,
// every exception set up to AllExceptions and each HISS-04 statement policyFacts accepts
// for the policy the current harness states) is earlier output for the unpinned, unknown-fact synthesis except text
// equal to that synthesis. The combinations are matched against one enumeration (renderedPrior),
// then a sample end to end through PriorGenerated. A language or exception bit above its bound
// renders as the set without it and adds no new text.
func TestPriorGeneratedRecognisesThisReleaseUnderEveryFactCombination(t *testing.T) {
	current := synthesizeWidget(t, t.TempDir(), "", unknownFacts)
	stated := statedPolicyOf(current.Invariants)
	if len(stated.funcLOCs) != 1 || stated.funcLOCs[0] != config.AuditMaxFuncLOC || len(stated.complexity) != 1 {
		t.Fatalf("unknown-fact synthesis states %+v, want the audit ceiling %d and the HISS-04 defaults", stated, config.AuditMaxFuncLOC)
	}
	released, err := currentReleaseHarnesses(current.Platform, stated)
	if err != nil {
		t.Fatal(err)
	}
	combinations := (int(hisscatalog.AllLanguages) + 1) * (int(hisscatalog.AllExceptions) + 1) * len(policyFacts(stated))
	if len(released) != len(releasedPushRows())*len(releasedReceiptRows())*combinations*len(releasedRegisterDirectives()) {
		t.Fatalf("fact combinations = %d, want every released push row pair x receipt row x %d HISS fact combinations x each register directive",
			len(released), combinations)
	}
	self := 0
	for index := range released {
		isCurrent := sameHarness(&released[index], current)
		if isCurrent {
			self++
			continue
		}
		text, err := MarshalHarness(&released[index])
		if err != nil {
			t.Fatal(err)
		}
		if prior, err := renderedPrior(string(text), &released[index], released); err != nil || prior == nil {
			t.Fatalf("combination %d not recognised: prior=%v err=%v", index, prior, err)
		}
	}
	// Several sets render alike (every set carrying each clause's languages labels as the unknown
	// set does), so the current text recurs; it must occur at least once.
	if self == 0 {
		t.Fatal("current synthesis missing from the fact combinations")
	}
	for _, index := range []int{0, len(released) / 2, len(released) - 1} {
		repo := t.TempDir()
		writeRepoFile(t, repo, ".standards.yaml", widgetManifest)
		if err := WriteHarness(&released[index], repo); err != nil {
			t.Fatal(err)
		}
		state, err := PriorGenerated(context.Background(), repo, current)
		if err != nil || state.Generated == sameHarness(&released[index], current) {
			t.Fatalf("combination %d end to end: prior=%+v err=%v", index, state, err)
		}
	}
	beyond, err := catalogInvariants(hisscatalog.Facts{Languages: hisscatalog.AllLanguages + 1, Exceptions: hisscatalog.AllExceptions + 1})
	other, otherErr := catalogInvariants(hisscatalog.Facts{Languages: hisscatalog.LanguageOther})
	if err != nil || otherErr != nil || strings.Join(beyond, "\n") != strings.Join(other, "\n") {
		t.Fatalf("a bit above AllLanguages or AllExceptions renders text the enumeration misses: %v %v\n%q", err, otherErr, beyond)
	}
}

// TestStatedFuncLOCs reads the function lengths a harness states. Positive: each length, once,
// in order. Negative: text stating no length, or a zero or seven-digit one, yields none.
// Boundary: a list longer than maxHarnessValues is read only up to that bound.
func TestStatedFuncLOCs(t *testing.T) {
	got := statedFuncLOCs([]string{"HISS-04: func LOC <= 60 (audit ceiling)",
		"HISS-04: func LOC <= 50", "HISS-04: func LOC <= 60"})
	if len(got) != 2 || got[0] != 60 || got[1] != 50 {
		t.Fatalf("statedFuncLOCs = %v, want [60 50]", got)
	}
	if got := statedFuncLOCs([]string{"HISS-01: zero `goto`", "func LOC <= 0", "func LOC <= 1234567"}); len(got) != 0 {
		t.Fatalf("statedFuncLOCs of no valid length = %v", got)
	}
	long := make([]string, maxHarnessValues+1)
	long[maxHarnessValues] = "func LOC <= 42"
	if got := statedFuncLOCs(long); len(got) != 0 {
		t.Fatalf("statedFuncLOCs read past maxHarnessValues: %v", got)
	}
}

// TestLimitFacts states which function lengths the refresh key accepts. Positive: the audit
// ceiling in both of its forms, whatever the current synthesis states. Negative: no plain number
// but the current one, so the ceiling itself is never plain. Boundary: a current limit one
// below the ceiling adds its plain form; one at the ceiling adds nothing.
func TestLimitFacts(t *testing.T) {
	ceiling := config.AuditMaxFuncLOC
	rendered := func(limits []int) []string {
		var texts []string
		for _, facts := range limitFacts(limits) {
			invariants, err := catalogInvariants(facts)
			if err != nil {
				t.Fatal(err)
			}
			texts = append(texts, invariants[2])
		}
		return texts
	}
	unresolved := fmt.Sprintf("func LOC <= %d (audit ceiling; stricter repository policy wins)", ceiling)
	atCeiling := fmt.Sprintf("func LOC <= %d (audit ceiling)", ceiling)
	below := fmt.Sprintf("func LOC <= %d", ceiling-1)
	for _, tc := range []struct {
		limits []int
		want   []string
	}{
		{nil, []string{unresolved, atCeiling}},
		{[]int{ceiling}, []string{unresolved, atCeiling}},
		{[]int{ceiling - 1}, []string{unresolved, atCeiling, below}},
	} {
		got := rendered(tc.limits)
		if len(got) != len(tc.want) {
			t.Fatalf("limitFacts(%v) renders %q, want %q", tc.limits, got, tc.want)
		}
		for index, want := range tc.want {
			if !strings.HasSuffix(got[index], want) {
				t.Errorf("limitFacts(%v)[%d] renders %q, want suffix %q", tc.limits, index, got[index], want)
			}
		}
	}
}
