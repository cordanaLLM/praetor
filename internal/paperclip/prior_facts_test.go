// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// widgetManifest declares acme/widget; pinnedReceipt pins a well-formed receipt key.
const (
	widgetManifest = "repository:\n  owner: acme\n  name: widget\n"
	pinnedReceipt  = "receipt:\n  public_key: \"" + "abababababababababababababababababababababababababababababababab" + "\"\n"
)

// synthesizeWidget rewrites the manifest of repo with receiptSection and synthesizes its harness
// for languages, as one adoption run does.
func synthesizeWidget(t *testing.T, repo, receiptSection string, languages hisscatalog.Language) *Harness {
	t.Helper()
	writeRepoFile(t, repo, ".standards.yaml", widgetManifest+receiptSection)
	h, err := SynthesizeHarness(context.Background(), repo, languages)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// writtenWidget writes this release's harness for receiptSection and languages, then returns
// the repository after its facts changed to the next ones, with the harness synthesized there.
func writtenWidget(t *testing.T, before, after string, from, to hisscatalog.Language) (string, *Harness) {
	t.Helper()
	repo := t.TempDir()
	if err := WriteHarness(synthesizeWidget(t, repo, before, from), repo); err != nil {
		t.Fatal(err)
	}
	return repo, synthesizeWidget(t, repo, after, to)
}

// TestPriorGeneratedRecognisesThisReleaseAfterFactsChange: Positive. The unpinned receipt row
// tells the operator to pin a key; once pinned, the harness adoption wrote is still unmodified
// output, so plain adopt refreshes it to the row that requires receipts. The same holds when the
// repository gains a language, and in reverse.
func TestPriorGeneratedRecognisesThisReleaseAfterFactsChange(t *testing.T) {
	cases := map[string]struct {
		before, after string
		from, to      hisscatalog.Language
		want          string
	}{
		"pin key after adopt":  {"", pinnedReceipt, hisscatalog.LanguageGo, hisscatalog.LanguageGo, pinnedReceiptRow},
		"unpin key":            {pinnedReceipt, "", 0, 0, unpinnedReceiptPrefix},
		"Go gains Rust":        {"", "", hisscatalog.LanguageGo, hisscatalog.LanguageGo | hisscatalog.LanguageRust, "Rust: zero `.unwrap()`"},
		"Rust drops to none":   {"", "", hisscatalog.LanguageRust, 0, "Go: I/O takes `context.Context` deadline"},
		"pin key and add lang": {"", pinnedReceipt, 0, hisscatalog.LanguageC, pinnedReceiptRow},
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
// harness.json, keeps the harness operator-owned after the facts change.
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
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if err := WriteHarness(edit(synthesizeWidget(t, repo, "", hisscatalog.LanguageGo)), repo); err != nil {
				t.Fatal(err)
			}
			current := synthesizeWidget(t, repo, pinnedReceipt, hisscatalog.LanguageGo|hisscatalog.LanguageRust)
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
				t.Fatalf("%s read as unmodified earlier output: prior=%+v err=%v", name, state, err)
			}
		})
	}
	repo, current := writtenWidget(t, "", pinnedReceipt, hisscatalog.LanguageGo, hisscatalog.LanguageGo)
	writeRepoFile(t, repo, ".paperclip/rules.md", renderRules(current))
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
		t.Fatalf("rules.md of other facts beside harness.json read as earlier output: prior=%+v err=%v", state, err)
	}
}

// TestPriorGeneratedRecognisesThisReleaseUnderEveryFactCombination: Boundary. Every value the
// synthesis distinguishes, both receipt states times every language set up to AllLanguages, is
// earlier output for the unpinned, unknown-language synthesis except text equal to that synthesis;
// a language bit above AllLanguages renders as a set no clause names and adds no new text.
func TestPriorGeneratedRecognisesThisReleaseUnderEveryFactCombination(t *testing.T) {
	probeRepo := t.TempDir()
	current := synthesizeWidget(t, probeRepo, "", 0)
	released, err := currentReleaseHarnesses(current.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 2*(int(hisscatalog.AllLanguages)+1) {
		t.Fatalf("fact combinations = %d, want both receipt states x every language set", len(released))
	}
	self := 0
	for index := range released {
		repo := t.TempDir()
		writeRepoFile(t, repo, ".standards.yaml", widgetManifest)
		if err := WriteHarness(&released[index], repo); err != nil {
			t.Fatal(err)
		}
		state, err := PriorGenerated(context.Background(), repo, current)
		isCurrent := released[index].OperatingContract[3] == current.OperatingContract[3] &&
			strings.Join(released[index].Invariants, "\n") == strings.Join(current.Invariants, "\n")
		if isCurrent {
			self++
		}
		if err != nil || state.Generated == isCurrent {
			t.Fatalf("combination %d (current=%v): prior=%+v err=%v", index, isCurrent, state, err)
		}
	}
	// Several sets render alike (every set carrying each clause's languages labels as the unknown
	// set does), so the current text recurs; it must occur at least once.
	if self == 0 {
		t.Fatal("current synthesis missing from the fact combinations")
	}
	beyond, err := catalogInvariants(hisscatalog.AllLanguages + 1)
	other, otherErr := catalogInvariants(hisscatalog.LanguageOther)
	if err != nil || otherErr != nil || strings.Join(beyond, "\n") != strings.Join(other, "\n") {
		t.Fatalf("a bit above AllLanguages renders text the enumeration misses: %v %v\n%q", err, otherErr, beyond)
	}
}
