// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"strconv"
	"strings"
)

// Language is a set of source languages an adopted repository carries. A directive clause
// that names a construct of one language (Go's context.Context, Rust's unwrap, C's libc)
// renders only for a repository carrying that language, so a Rust or TypeScript repository
// is never told to use a Go API (#68).
type Language uint8

const (
	// LanguageGo is Go (go.mod).
	LanguageGo Language = 1 << iota
	// LanguageRust is Rust (Cargo.toml).
	LanguageRust
	// LanguageC is C and C++ (a native build: meson or CMake).
	LanguageC
	// LanguagePython is Python.
	LanguagePython
	// LanguageOther is a detected language no clause names, such as TypeScript or Java. It
	// makes the set known, so no labelled clause renders for it.
	LanguageOther
)

// languageNames labels the languages in the order a clause names them.
var languageNames = [...]struct {
	language Language
	name     string
}{
	{LanguageGo, "Go"},
	{LanguageRust, "Rust"},
	{LanguageC, "C/C++"},
	{LanguagePython, "Python"},
}

// CeilingFuncLOC is the HISS-04 function-length ceiling the audit-compatibility layer applies
// to every adopted repository (config.AuditMaxFuncLOC; the catalog may not import config, so
// TestAdoptedDirective_Negative_ComplexityMatchesTheAuditCeiling pins the two together).
const CeilingFuncLOC = 60

// Clause is one part of a rule's adopted directive.
type Clause struct {
	// Languages limits the clause to repositories carrying one of them; zero means every
	// repository.
	Languages Language
	// Text is the clause, agent-only text in the internal register.
	Text string
	// FuncLOC completes Text with the function-length limit the repository's audit enforces
	// (Facts.MaxFuncLOC), so HISS-04 states the effective limit rather than a copy of it.
	FuncLOC bool
}

// Facts is what one adopted repository's rows depend on.
type Facts struct {
	// Languages the repository carries; zero means unknown, and every language clause then
	// renders with its language label so no rule is lost.
	Languages Language
	// MaxFuncLOC is the function length the repository's audit enforces; zero means not
	// resolved, and the row states CeilingFuncLOC as the ceiling a policy may tighten.
	MaxFuncLOC int
}

// noAnalogue is the directive of a rule none of whose clauses applies to the repository's
// languages, such as HISS-09's `unsafe` proof in a TypeScript repository.
const noAnalogue = "n/a: no such construct in repository languages; advisory"

// AdoptedDirective renders the rule's directive for one adopted repository: every clause for
// all languages, each language clause for a repository carrying its language (labelled with
// it), and the effective function-length limit. A rule left with no clause says it has no
// analogue there rather than asserting one.
func (r Rule) AdoptedDirective(f Facts) string {
	parts := make([]string, 0, len(r.Directive))
	for _, clause := range r.Directive {
		if text, ok := clause.render(f); ok {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return noAnalogue
	}
	return strings.Join(parts, "; ")
}

// render states one clause for f, or reports false when it does not apply.
func (c Clause) render(f Facts) (string, bool) {
	text := c.Text
	if c.FuncLOC {
		text += funcLOCLimit(f.MaxFuncLOC)
	}
	if c.Languages == 0 {
		return text, true
	}
	shown := c.Languages
	if f.Languages != 0 {
		shown &= f.Languages
	}
	if shown == 0 {
		return "", false
	}
	return languageLabel(shown) + ": " + text, true
}

// funcLOCLimit completes the HISS-04 function-length clause.
func funcLOCLimit(limit int) string {
	if limit > 0 {
		return " " + strconv.Itoa(limit)
	}
	return " " + strconv.Itoa(CeilingFuncLOC) + " (audit ceiling; stricter repository policy wins)"
}

// languageLabel names the languages of set, in languageNames order.
func languageLabel(set Language) string {
	names := make([]string, 0, len(languageNames))
	for _, entry := range languageNames {
		if set&entry.language != 0 {
			names = append(names, entry.name)
		}
	}
	return strings.Join(names, ", ")
}
