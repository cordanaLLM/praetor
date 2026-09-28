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
	if got := directiveOf(t, "HISS-04", Facts{MaxFuncLOC: 50, CeilingFuncLOC: 60}); !strings.HasSuffix(got, "; func LOC <= 50") {
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

// cleanupGoto is HISS-01's exception clause as a C repository declaring it reads it.
const cleanupGoto = "C/C++: `goto` only single-level forward jump to function cleanup label (declared exception); audit still reports each `goto`"

// TestAdoptedDirective_Positive_DeclaredExceptionReplacesTheBan: a C repository that declares
// its single-level cleanup `goto` reads that exception, not the blanket zero-`goto` ban, and is
// told the audit still reports each `goto`; one that declares nothing keeps the ban (#68).
func TestAdoptedDirective_Positive_DeclaredExceptionReplacesTheBan(t *testing.T) {
	declared := directiveOf(t, "HISS-01", Facts{Languages: LanguageC, Exceptions: ExceptionCleanupGoto})
	if want := "recursion prohibited; call graph = DAG; " + cleanupGoto; declared != want {
		t.Errorf("HISS-01 for C declaring the exception = %q, want %q", declared, want)
	}
	if plain := directiveOf(t, "HISS-01", Facts{Languages: LanguageC}); plain != "recursion prohibited; call graph = DAG; C/C++: zero `goto`" {
		t.Errorf("HISS-01 for C declaring nothing = %q", plain)
	}
}

// TestAdoptedDirective_Negative_ExceptionStaysInItsLanguageAndRule: the C exception never
// reaches a Go-only repository, which keeps Go's own ban, and no other rule's directive changes
// when a repository declares it.
func TestAdoptedDirective_Negative_ExceptionStaysInItsLanguageAndRule(t *testing.T) {
	for _, exceptions := range []Exception{0, ExceptionCleanupGoto} {
		got := directiveOf(t, "HISS-01", Facts{Languages: LanguageGo, Exceptions: exceptions})
		if got != "recursion prohibited; call graph = DAG; Go: zero `goto`" {
			t.Errorf("HISS-01 for Go with exceptions %b = %q", exceptions, got)
		}
	}
	for _, rule := range Rules() {
		for _, language := range []Language{0, LanguageC, LanguageGo | LanguageC, LanguageOther} {
			plain := rule.AdoptedDirective(Facts{Languages: language})
			declared := rule.AdoptedDirective(Facts{Languages: language, Exceptions: ExceptionCleanupGoto})
			if rule.ID != "HISS-01" && plain != declared {
				t.Errorf("%s changes with the cleanup-goto exception: %q -> %q", rule.ID, plain, declared)
			}
		}
	}
}

// TestAdoptedDirective_Boundary_ExceptionWithUnknownOrMixedLanguages: with unknown languages, or
// Go and C together, the exception lifts only C's share of the ban, so Go's stays labelled beside
// it; an exception bit above AllExceptions changes nothing; every variant lints clean.
func TestAdoptedDirective_Boundary_ExceptionWithUnknownOrMixedLanguages(t *testing.T) {
	want := "recursion prohibited; call graph = DAG; Go: zero `goto`; " + cleanupGoto
	for _, language := range []Language{0, LanguageGo | LanguageC} {
		if got := directiveOf(t, "HISS-01", Facts{Languages: language, Exceptions: ExceptionCleanupGoto}); got != want {
			t.Errorf("HISS-01 for %b declaring the exception = %q, want %q", language, got, want)
		}
	}
	beyond := AllExceptions + 1
	if allDirectives(Facts{Languages: LanguageC, Exceptions: beyond}) != allDirectives(Facts{Languages: LanguageC}) {
		t.Error("a bit above AllExceptions changes a directive")
	}
	if beyond&AllExceptions != 0 || AllExceptions&ExceptionCleanupGoto == 0 {
		t.Errorf("AllExceptions %b is not the contiguous run of bits from ExceptionCleanupGoto", AllExceptions)
	}
	var text strings.Builder
	for _, language := range []Language{0, LanguageC, LanguageGo | LanguageC} {
		text.WriteString(allDirectives(Facts{Languages: language, Exceptions: AllExceptions}))
	}
	if report := caveman.Check(text.String(), caveman.Options{}); !report.Passed() {
		t.Errorf("exception directives fail the lint: %+v", report.Findings)
	}
}

// TestFuncLOCLimit states the function length the audit enforces. Positive: a resolved limit
// below the audit ceiling is stated alone, and one at the ceiling says the ceiling caps the
// profile's value (a profile snapshot can say 75 where the audit enforces 60). Negative: an
// unresolved policy never states a resolved number; it states the ceiling the caller read and
// that a stricter policy wins, or, with no ceiling either, where the audit prints the limit.
// Boundary: a limit one below the ceiling is plain. Every variant lints clean.
func TestFuncLOCLimit(t *testing.T) {
	cases := []struct {
		facts Facts
		want  string
	}{
		{Facts{MaxFuncLOC: 50, CeilingFuncLOC: 60}, " 50"},
		{Facts{MaxFuncLOC: 60, CeilingFuncLOC: 60}, " 60 (audit ceiling; caps profile value)"},
		{Facts{CeilingFuncLOC: 60}, " 60 (audit ceiling; stricter repository policy wins)"},
		{Facts{}, " repository audit limit (`praetorctl audit` prints `max_func_loc`)"},
		{Facts{MaxFuncLOC: 59, CeilingFuncLOC: 60}, " 59"},
	}
	var text strings.Builder
	for _, tc := range cases {
		if got := funcLOCLimit(tc.facts); got != tc.want {
			t.Errorf("funcLOCLimit(%+v) = %q, want %q", tc.facts, got, tc.want)
		}
		text.WriteString(directiveOf(t, "HISS-04", tc.facts) + ".\n")
	}
	if report := caveman.Check(text.String(), caveman.Options{}); !report.Passed() {
		t.Errorf("function-length variants fail the lint: %+v", report.Findings)
	}
}
