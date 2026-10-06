// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ExceptionRuleClangTidyCoverage is the rule of the clang-tidy translation-unit coverage gate
// (internal/tidycoverage): an entry excuses a tracked translation unit that no declared
// clang-tidy lane reads.
const ExceptionRuleClangTidyCoverage = "clang-tidy-coverage"

// exceptionRules lists the rules an exceptions entry may name. Each one is a gate that reads
// the list, so an entry naming any other rule would excuse nothing and is refused instead.
var exceptionRules = []string{ExceptionRuleClangTidyCoverage}

// Bounds of the exceptions list (HISS-02).
const (
	// MaxExceptions bounds the entries one manifest declares.
	MaxExceptions = 1024
	// MaxExceptionDays is how far ahead of today an entry may expire. An exception is a debt
	// with a due date, reviewed again when it falls due; scripts/npm_audit_gate.py applies the
	// same bound to its own list.
	MaxExceptionDays = 90
	// MaxExceptionReasonBytes bounds one entry's reason.
	MaxExceptionReasonBytes = 1024
	// maxExceptionPathSegments bounds the segments of a path an entry is matched against.
	maxExceptionPathSegments = 256
)

// ExceptionDateLayout is the form of an entry's expires date.
const ExceptionDateLayout = "2006-01-02"

// globCharacters are the characters util.MatchGlobSegments reads as pattern syntax. A path
// entry names one file, so it may carry none of them.
const globCharacters = "*?[]\\"

// Exception is one entry of the manifest's exceptions list: one standard (Rule) waived for one
// file (Path) or for the files one glob matches (Glob), with the reason and the day after
// which the waiver stops holding (Expires, YYYY-MM-DD). The gate that owns Rule reads the
// entry; an expired entry excuses nothing, so the gate fails on its files as if the entry were
// missing (AGENTS.md rule 14).
type Exception struct {
	Rule    string `yaml:"rule"`
	Path    string `yaml:"path,omitempty"`
	Glob    string `yaml:"glob,omitempty"`
	Reason  string `yaml:"reason"`
	Expires string `yaml:"expires"`
}

// Target returns the path or glob the entry names.
func (e Exception) Target() string {
	if e.Path != "" {
		return e.Path
	}
	return e.Glob
}

// Expired reports whether the entry stopped holding before today; it still holds on its
// expires day. An expires value that is no date counts as expired, so an entry that slipped
// past validation excuses nothing.
func (e Exception) Expired(today time.Time) bool {
	expires, err := time.Parse(ExceptionDateLayout, e.Expires)
	return err != nil || expires.Before(ExceptionDay(today))
}

// Matches reports whether the entry names the slash-separated repository path rel: the same
// path, or a glob that matches it in full under util.MatchGlobSegments ("*" stays inside one
// segment, a "**" segment spans any number).
func (e Exception) Matches(rel string) bool {
	if e.Path != "" {
		return e.Path == rel
	}
	segments := strings.Split(rel, "/")
	if e.Glob == "" || len(segments) > maxExceptionPathSegments {
		return false
	}
	return util.MatchGlobSegments(strings.Split(e.Glob, "/"), segments)
}

// ExceptionDay returns the calendar day of t, in t's location, as midnight UTC: the form an
// entry's expires date parses to, so the two compare as days.
func ExceptionDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ExceptionsFor returns the entries of entries that name rule, in declaration order: what the
// gate owning rule reads from the manifest's exceptions list.
func ExceptionsFor(entries []Exception, rule string) []Exception {
	var selected []Exception
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		if entries[index].Rule == rule {
			selected = append(selected, entries[index])
		}
	}
	return selected
}

// ValidateExceptions refuses an exceptions list a gate could not apply as written: more than
// MaxExceptions entries, an unknown rule, an entry naming both or neither of path and glob, a
// path that is not one clean repository-relative file, a glob outside the StyleExclusionProblem
// rules, an empty or multi-line reason, an expires value that is no YYYY-MM-DD date or lies more
// than MaxExceptionDays after today, and a repeated entry. An expired entry is valid: the gate
// that owns its rule fails on its files instead.
func ValidateExceptions(entries []Exception, today time.Time) error {
	if len(entries) > MaxExceptions {
		return fmt.Errorf("exceptions has %d entries; maximum is %d", len(entries), MaxExceptions)
	}
	seen := make(map[[3]string]int, len(entries))
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		entry := entries[index]
		if problem := entry.problem(today); problem != "" {
			return fmt.Errorf("exceptions[%d] %s", index, problem)
		}
		key := [3]string{entry.Rule, entry.Path, entry.Glob}
		if first, repeated := seen[key]; repeated {
			return fmt.Errorf("exceptions[%d] repeats exceptions[%d] (rule %s, %s)", index, first, entry.Rule, entry.Target())
		}
		seen[key] = index
	}
	return nil
}

// problem names why one entry is refused, or returns "".
func (e Exception) problem(today time.Time) string {
	if !slices.Contains(exceptionRules, e.Rule) {
		return fmt.Sprintf("rule %q is not a rule any gate reads from this list (known: %s)", e.Rule, strings.Join(exceptionRules, ", "))
	}
	if problem := e.targetProblem(); problem != "" {
		return problem
	}
	if problem := exceptionReasonProblem(e.Reason); problem != "" {
		return "reason " + problem
	}
	return exceptionExpiryProblem(e.Expires, today)
}

// targetProblem requires exactly one of path and glob, each in its repository-relative form.
func (e Exception) targetProblem() string {
	switch {
	case (e.Path == "") == (e.Glob == ""):
		return "must name exactly one of path and glob"
	case e.Path != "" && (!validRepositoryPath(e.Path) || strings.ContainsAny(e.Path, globCharacters)):
		return fmt.Sprintf("path %q must be one clean repository-relative file path of at most %d bytes, without glob characters",
			e.Path, maxRepositoryPath)
	case e.Glob != "":
		if problem := StyleExclusionProblem(e.Glob); problem != "" {
			return fmt.Sprintf("glob %q %s", e.Glob, problem)
		}
	}
	return ""
}

// exceptionReasonProblem requires one non-empty line of at most MaxExceptionReasonBytes.
func exceptionReasonProblem(reason string) string {
	switch {
	case strings.TrimSpace(reason) == "":
		return "must say why the standard cannot hold for the file"
	case len(reason) > MaxExceptionReasonBytes:
		return fmt.Sprintf("exceeds %d bytes", MaxExceptionReasonBytes)
	case strings.ContainsFunc(reason, unicode.IsControl):
		return "must be one line without control characters"
	}
	return ""
}

// exceptionExpiryProblem requires a YYYY-MM-DD date at most MaxExceptionDays after today.
func exceptionExpiryProblem(expires string, today time.Time) string {
	date, err := time.Parse(ExceptionDateLayout, expires)
	if err != nil {
		return fmt.Sprintf("expires %q must be a YYYY-MM-DD date", expires)
	}
	if latest := ExceptionDay(today).AddDate(0, 0, MaxExceptionDays); date.After(latest) {
		return fmt.Sprintf("expires %s is more than %d days after today; the latest date allowed is %s",
			expires, MaxExceptionDays, latest.Format(ExceptionDateLayout))
	}
	return ""
}
