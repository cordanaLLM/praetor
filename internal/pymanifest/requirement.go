// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package pymanifest reads the PEP 508 requirements of Python dependency manifests: a line of a
// requirements file (requirements.in, requirements.txt) and an entry of a pyproject.toml or
// setup.py dependency list. It is the one requirement reader: the needs analyzer and the credits
// inventory (internal/supplychain) both call it, so they read every line the same way.
package pymanifest

import "strings"

// Requirement is one PEP 508 requirement, read as far as the dependency readers need it.
type Requirement struct {
	// Name is the distribution name, lower-cased: pip compares names case-insensitively.
	Name string
	// Version is the version specifier without its first comparison operator ("2.0" for
	// "==2.0", "1,<2" for ">=1,<2"), or "" when the requirement pins no version: a bare name
	// or a direct reference ("name @ https://...").
	Version string
}

// specifierOperators are the comparison operators a version specifier starts with, longest
// first, so "===" is not read as "==" followed by "=". A lone "=" is not PEP 440, but pip's
// legacy readers accepted it and so does this one.
var specifierOperators = []string{"===", "==", ">=", "<=", "~=", "!=", ">", "<", "="}

// ParseRequirement reads one requirement line. A comment, an environment marker and extras are
// discarded, and surrounding quotes and a trailing comma are trimmed, so a pyproject.toml list
// entry (`"requests[socks]>=2",`) reads like a requirements-file line. It reports false for a
// line that names no distribution: a blank line, a comment, an option line (-r base.in, -e .,
// --index-url) or a fragment that starts with an operator ("<2" of a list split at commas).
func ParseRequirement(line string) (Requirement, bool) {
	spec := stripDecorations(line)
	if strings.HasPrefix(spec, "-") {
		return Requirement{}, false
	}
	end := strings.IndexFunc(spec, func(r rune) bool { return !isNameRune(r) })
	if end < 0 {
		end = len(spec)
	}
	if end == 0 {
		return Requirement{}, false
	}
	return Requirement{Name: strings.ToLower(spec[:end]), Version: specifierVersion(spec[end:])}, true
}

// stripDecorations removes a comment and an environment marker from a requirement and trims the
// quotes and comma a TOML or Python list puts around it:
// `"requests[security]>=2 ; python_version<'3.8'",  # note` becomes `requests[security]>=2`.
func stripDecorations(line string) string {
	spec, _, _ := strings.Cut(line, "#")
	spec, _, _ = strings.Cut(spec, ";")
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(spec), `"',`))
}

// isNameRune reports whether r may appear in a PEP 508 distribution name: an ASCII letter or
// digit, ".", "_" or "-".
func isNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '.' || r == '_' || r == '-'
}

// specifierVersion returns the version a requirement pins from what follows its name: extras are
// skipped, the parentheses of the legacy "name (>=1.0)" form removed, and the first comparison
// operator cut. Anything that starts with no operator, a direct reference among them, pins none.
func specifierVersion(rest string) string {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "[") {
		_, after, closed := strings.Cut(rest, "]")
		if !closed {
			return ""
		}
		rest = strings.TrimSpace(after)
	}
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")"))
	for _, operator := range specifierOperators {
		if version, found := strings.CutPrefix(rest, operator); found {
			return strings.TrimSpace(version)
		}
	}
	return ""
}
