// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// directiveOf renders one rule's adopted directive for f.
func directiveOf(t *testing.T, id string, f Facts) string {
	t.Helper()
	rule, ok := LookupRule(id)
	if !ok {
		t.Fatalf("no rule %s", id)
	}
	return rule.AdoptedDirective(f)
}

// allDirectives renders every rule's adopted directive for f, one sentence per line.
func allDirectives(f Facts) string {
	var b strings.Builder
	for _, rule := range Rules() {
		b.WriteString(rule.ID + " " + rule.AdoptedDirective(f) + ".\n")
	}
	return b.String()
}

// TestAdoptedDirective_Positive_LanguageClausesFollowTheRepository: a Go repository reads the
// context.Context deadline and the unchecked-error clause, a Rust repository the unwrap clause,
// a C repository the goto and libc clauses, each labelled with its language (#68).
func TestAdoptedDirective_Positive_LanguageClausesFollowTheRepository(t *testing.T) {
	cases := []struct {
		language Language
		id, want string
	}{
		{LanguageGo, "HISS-02", "scalar upper bound on every loop; explicit deadline on every I/O call; Go: I/O takes `context.Context` deadline"},
		{LanguageGo, "HISS-07", "every error handled or wrapped with context; Go: zero unchecked `error` return"},
		{LanguageRust, "HISS-07", "every error handled or wrapped with context; Rust: zero `.unwrap()` / `.expect()` outside tests"},
		{LanguageRust, "HISS-09", "Rust: `// SAFETY:` proof before every `unsafe` block"},
		{LanguageC, "HISS-01", "recursion prohibited; call graph = DAG; C/C++: zero `goto`"},
		{LanguageC, "HISS-08", "zero dynamic code execution (`eval` / `exec`); C/C++: zero banned libc (`gets` / `strcpy` / `sprintf`)"},
	}
	for _, tc := range cases {
		if got := directiveOf(t, tc.id, Facts{Languages: tc.language}); got != tc.want {
			t.Errorf("%s for %s = %q, want %q", tc.id, languageLabel(tc.language), got, tc.want)
		}
	}
}

// TestAdoptedDirective_Negative_NoForeignLanguageWording: no Go wording reaches a Rust or C
// repository, no Rust wording a Go-only one, and a repository of languages no clause names
// reads no labelled clause at all; a rule left without a clause says it has no analogue.
func TestAdoptedDirective_Negative_NoForeignLanguageWording(t *testing.T) {
	forbidden := map[Language][]string{
		LanguageRust:  {"context.Context", "Go:", "`goto`", "unchecked `error`", "libc"},
		LanguageC:     {"context.Context", "Go:", ".unwrap()", "Rust:", "unsafe"},
		LanguageGo:    {".unwrap()", "Rust:", "libc", "C/C++:"},
		LanguageOther: {"Go:", "Rust:", "C/C++:", "Python:", "context.Context", ".unwrap()"},
	}
	for language, words := range forbidden {
		text := allDirectives(Facts{Languages: language})
		for _, word := range words {
			if strings.Contains(text, word) {
				t.Errorf("%b repository reads %q:\n%s", language, word, text)
			}
		}
	}
	for _, id := range []string{"HISS-03", "HISS-09"} {
		if got := directiveOf(t, id, Facts{Languages: LanguageOther}); got != noAnalogue {
			t.Errorf("%s for an unmodelled language = %q, want %q", id, got, noAnalogue)
		}
	}
}

// TestAdoptedDirective_Boundary_UnknownLanguagesAndLimit: with no known language every clause
// renders with its label, so no rule is dropped; a mixed repository reads the clauses of each
// language it carries; a resolved function-length limit replaces the ceiling note; and every
// variant lints clean in the internal register.
func TestAdoptedDirective_Boundary_UnknownLanguagesAndLimit(t *testing.T) {
	if got := directiveOf(t, "HISS-01", Facts{}); got != "recursion prohibited; call graph = DAG; Go, C/C++: zero `goto`" {
		t.Errorf("HISS-01 with unknown languages = %q", got)
	}
	if got := directiveOf(t, "HISS-09", Facts{Languages: LanguageGo | LanguageRust | LanguagePython}); got != "Go, Rust: `// SAFETY:` proof before every `unsafe` block" {
		t.Errorf("HISS-09 for Go + Rust + Python = %q", got)
	}
	if got := directiveOf(t, "HISS-04", Facts{MaxFuncLOC: 50}); !strings.HasSuffix(got, "; func LOC <= 50") {
		t.Errorf("HISS-04 with a resolved limit = %q", got)
	}
	var text strings.Builder
	for _, language := range []Language{0, LanguageGo, LanguageRust, LanguageC, LanguagePython, LanguageOther} {
		text.WriteString(allDirectives(Facts{Languages: language}))
	}
	if report := caveman.Check(text.String(), caveman.Options{}); !report.Passed() {
		t.Errorf("adopted directives fail the lint: %+v", report.Findings)
	}
}

// TestAllLanguages covers the enumeration bound the Paperclip refresh key walks. Positive: every
// named language and LanguageOther lie inside it. Negative: no bit above it names a language, so
// a set carrying one renders as the set without it. Boundary: its bits are contiguous from Go,
// so zero through AllLanguages is exactly every language set.
func TestAllLanguages(t *testing.T) {
	for _, entry := range languageNames {
		if AllLanguages&entry.language != entry.language {
			t.Errorf("%s outside AllLanguages %b", entry.name, AllLanguages)
		}
	}
	if AllLanguages&LanguageOther == 0 {
		t.Errorf("LanguageOther outside AllLanguages %b", AllLanguages)
	}
	beyond := AllLanguages + 1
	if allDirectives(Facts{Languages: beyond | LanguageRust}) != allDirectives(Facts{Languages: LanguageRust}) {
		t.Error("a bit above AllLanguages changes a directive")
	}
	if beyond&AllLanguages != 0 || AllLanguages&LanguageGo == 0 {
		t.Errorf("AllLanguages %b is not the contiguous run of bits from LanguageGo", AllLanguages)
	}
}
