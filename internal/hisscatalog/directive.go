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
	// languageEnd follows the last language bit; a language added above it joins AllLanguages.
	languageEnd
)

// AllLanguages is every language bit. Every value from zero to AllLanguages is a language set a
// repository can carry, so a caller that must recognise a rendering under any set (the Paperclip
// refresh key) enumerates exactly that bounded range.
const AllLanguages = languageEnd - 1

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

// Exception is a set of exceptions to adopted directives that a repository declares and
// documents itself. A declared exception replaces the clause it waives, for the languages it
// names, with the exception's own clause, so a repository whose standard allows a construct is
// not handed a blanket ban contradicting it (#68). The audit honours the same declaration, and
// the exception's clause renders the rule text the audit's check publishes (hiss.CleanupGotoRule),
// so the harness states exactly what the audit accepts.
type Exception uint8

const (
	// ExceptionCleanupGoto is a C/C++ `goto` jumping forward to the one cleanup label of its
	// function (single-level error unwinding), declared as hiss.exceptions.c_goto_cleanup in
	// .standards.yaml; hiss.CleanupGoto is the exact rule.
	ExceptionCleanupGoto Exception = 1 << iota
	// exceptionEnd follows the last exception bit; one added above it joins AllExceptions.
	exceptionEnd
)

// AllExceptions is every exception bit. Every value from zero to AllExceptions is an exception
// set a repository can declare, the bounded range the Paperclip refresh key enumerates.
const AllExceptions = exceptionEnd - 1

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
	// Waiver is the exception that lifts this language clause, for the languages of the rule's
	// clause stating that exception, once a repository declares it.
	Waiver Exception
	// Exception makes the clause state that exception: it renders only for a repository
	// declaring it, in place of the clause it waives.
	Exception Exception
}

// Facts is what one adopted repository's rows depend on.
type Facts struct {
	// Languages the repository carries; zero means unknown, and every language clause then
	// renders with its language label so no rule is lost.
	Languages Language
	// MaxFuncLOC is the function length the repository's audit enforces, read from the
	// effective policy the audit resolves; zero means not resolved.
	MaxFuncLOC int
	// CeilingFuncLOC is the function length the audit-compatibility layer caps every policy at
	// (config.AuditMaxFuncLOC). The caller reads it from config; the catalog keeps no copy.
	CeilingFuncLOC int
	// Exceptions are the exceptions the repository declares and documents.
	Exceptions Exception
}

// noAnalogue is the directive of a rule none of whose clauses applies to the repository's
// languages, such as HISS-09's `unsafe` proof in a TypeScript repository.
const noAnalogue = "n/a: no such construct in repository languages; advisory"

// AdoptedDirective renders the rule's directive for one adopted repository: every clause for
// all languages, each language clause for a repository carrying its language (labelled with
// it), a declared exception in place of the clause it waives, and the effective
// function-length limit. A rule left with no clause says it has no analogue there rather than
// asserting one.
func (r Rule) AdoptedDirective(f Facts) string {
	parts := make([]string, 0, len(r.Directive))
	for _, clause := range r.Directive {
		if text, ok := clause.render(f, r.exceptionLanguages(clause.Waiver&f.Exceptions)); ok {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return noAnalogue
	}
	return strings.Join(parts, "; ")
}

// exceptionLanguages is the languages the rule's clauses stating any of declared cover: the
// languages a clause those exceptions waive no longer claims.
func (r Rule) exceptionLanguages(declared Exception) Language {
	var languages Language
	if declared == 0 {
		return languages
	}
	for _, clause := range r.Directive {
		if clause.Exception&declared != 0 {
			languages |= clause.Languages
		}
	}
	return languages
}

// render states one clause for f, less the waived languages, or reports false when it does
// not apply.
func (c Clause) render(f Facts, waived Language) (string, bool) {
	if c.Exception != 0 && c.Exception&f.Exceptions == 0 {
		return "", false
	}
	text := c.Text
	if c.FuncLOC {
		text += funcLOCLimit(f)
	}
	if c.Languages == 0 {
		return text, true
	}
	shown := c.Languages &^ waived
	if f.Languages != 0 {
		shown &= f.Languages
	}
	if shown == 0 {
		return "", false
	}
	return languageLabel(shown) + ": " + text, true
}

// funcLOCLimit completes the HISS-04 function-length clause with the limit the audit enforces.
// At the audit ceiling it names the ceiling, which explains a pinned profile snapshot stating a
// higher value; it never claims the ceiling capped anything, since the facts do not say whether
// the profile declared more. Before the policy resolves it states the ceiling, which a stricter
// repository policy tightens.
func funcLOCLimit(f Facts) string {
	switch {
	case f.MaxFuncLOC > 0 && f.MaxFuncLOC == f.CeilingFuncLOC:
		return " " + strconv.Itoa(f.MaxFuncLOC) + " (audit ceiling)"
	case f.MaxFuncLOC > 0:
		return " " + strconv.Itoa(f.MaxFuncLOC)
	case f.CeilingFuncLOC > 0:
		return " " + strconv.Itoa(f.CeilingFuncLOC) + " (audit ceiling; stricter repository policy wins)"
	default:
		return " repository audit limit (`praetorctl audit` prints `max_func_loc`)"
	}
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
