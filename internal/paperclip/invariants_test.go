// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// invariantsFor synthesizes the harness of an acme/widget repository for languages and returns
// its invariants joined one per line.
func invariantsFor(t *testing.T, languages hisscatalog.Language) string {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: widget\n")
	h, err := SynthesizeHarness(t.Context(), repo, languages)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Invariants) != len(harnessInvariants) {
		t.Fatalf("harness carries %d invariants, want %d", len(h.Invariants), len(harnessInvariants))
	}
	return strings.Join(h.Invariants, "\n")
}

// TestSynthesizeHarness_Positive_InvariantsFollowLanguages: the invariants are the catalog's
// adopted directives, so a Go repository reads the context.Context deadline and a Rust one the
// unwrap ban, each labelled with its language (#68).
func TestSynthesizeHarness_Positive_InvariantsFollowLanguages(t *testing.T) {
	goText := invariantsFor(t, hisscatalog.LanguageGo)
	rustText := invariantsFor(t, hisscatalog.LanguageRust)
	for text, want := range map[string]string{
		goText:   "HISS-02: scalar upper bound on every loop; explicit deadline on every I/O call; Go: I/O takes `context.Context` deadline",
		rustText: "HISS-07: every error handled or wrapped with context; Rust: zero `.unwrap()` / `.expect()` outside tests",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("invariants lack %q:\n%s", want, text)
		}
	}
}

// TestSynthesizeHarness_Negative_NoForeignLanguageOrStaleLimit: a Rust or C repository gets no
// Go wording, a Go one no Rust wording, and no harness restates the 75-line function length the
// audit does not enforce.
func TestSynthesizeHarness_Negative_NoForeignLanguageOrStaleLimit(t *testing.T) {
	for language, words := range map[hisscatalog.Language][]string{
		hisscatalog.LanguageRust: {"context.Context", "Go:"},
		hisscatalog.LanguageC:    {"context.Context", "Go:", ".unwrap()"},
		hisscatalog.LanguageGo:   {".unwrap()", "Rust:"},
	} {
		text := invariantsFor(t, language)
		for _, word := range append(words, "75") {
			if strings.Contains(text, word) {
				t.Errorf("%b invariants carry %q:\n%s", language, word, text)
			}
		}
	}
}

// TestSynthesizeHarness_Boundary_UnknownLanguagesLabelEveryClause: with no known language every
// clause renders labelled, and HISS-04 states the audit ceiling a policy may tighten.
func TestSynthesizeHarness_Boundary_UnknownLanguagesLabelEveryClause(t *testing.T) {
	text := invariantsFor(t, 0)
	for _, want := range []string{"Go: I/O takes `context.Context` deadline", "Rust: zero `.unwrap()`", "Go, C/C++: zero `goto`",
		"HISS-04: McCabe cyclomatic <= 10, cognitive <= 15, statements <= 50; func LOC <= 60 (audit ceiling; stricter repository policy wins)"} {
		if !strings.Contains(text, want) {
			t.Errorf("unknown-language invariants lack %q:\n%s", want, text)
		}
	}
}
