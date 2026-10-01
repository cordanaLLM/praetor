// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// TOMLTableName normalizes a TOML table header line to its name with the spaces TOML permits
// removed and a trailing comment dropped: "[ extend ]" is "extend" and "[[ rules ]]  # x" is
// "[rules]", an array-of-tables header keeping one bracket pair. It reads a line's shape and is
// not a parser: the module carries no TOML library, and the callers (the gitleaks configuration
// check, the Cargo.toml edition and workspace members read in internal/flavor, the Cargo.toml
// and pyproject.toml dependency reads in internal/needs, and the REUSE.toml read of
// internal/supplychain) need only the header, single-line keys (TOMLKeyValue), inline tables
// (TOMLInlineTableFields) and arrays of strings (TOMLStringArray).
func TOMLTableName(header string) string {
	name, _, _ := strings.Cut(header, "#")
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	return strings.Trim(name, `"'`)
}

// TOMLKeyValue splits one key/value line into its key, with the spaces TOML permits around the
// dots of a dotted key removed ("package . edition" is "package.edition"), and the value text
// after the equals sign, trimmed. A line with no equals sign, or nothing before it, is not an
// assignment. Like TOMLTableName it reads the line's shape: a quoted key holding an equals sign
// is split there, and a key is never unquoted.
func TOMLKeyValue(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	key = strings.ReplaceAll(strings.TrimSpace(key), " ", "")
	if !ok || key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

// TOMLInlineTableFields calls visit with the key and the value text of each field of the inline
// table value opens ({ version = "1", path = "../core" }), in order, each split by TOMLKeyValue,
// value being what TOMLKeyValue returns after the equals sign. It reports whether value opens an
// inline table. Like the other reads here it takes the line's shape: the fields run to the
// first closing brace, or to the end of the line when the table does not close on it, and split
// at every comma, so an array field such as features = ["a", "b"] yields its first fragment and
// a fragment with no equals sign is skipped. The Cargo.toml reads of internal/needs (dependency
// tables) and internal/flavor (an inherited edition) share it.
func TOMLInlineTableFields(value string, visit func(key, fieldValue string)) bool {
	body, opened := strings.CutPrefix(value, "{")
	if !opened {
		return false
	}
	body, _, _ = strings.Cut(body, "}")
	for field := range strings.SplitSeq(body, ",") {
		if key, fieldValue, ok := TOMLKeyValue(field); ok {
			visit(key, fieldValue)
		}
	}
	return true
}

// TOMLStringValue returns the text of the single-line string value holds, value being what
// TOMLKeyValue returns after the equals sign: a basic ("...") or literal ('...') string followed
// by nothing or a comment. Anything else is refused rather than guessed at: a number, a boolean,
// an inline table, a multi-line string, and a basic string holding an escape sequence, which is
// not decoded.
func TOMLStringValue(value string) (string, bool) {
	text, rest, ok := cutTOMLString(value)
	rest = strings.TrimSpace(rest)
	if !ok || (rest != "" && !strings.HasPrefix(rest, "#")) {
		return "", false
	}
	return text, true
}

// TOMLStringArray reads the array of strings text opens, text being what TOMLKeyValue returns
// after the equals sign followed by the lines after it, each ended by "\n", so an array may
// span lines. It returns the strings, whether text reaches the closing bracket, and false when
// text is not such an array: it does not open with "[", an element is not a string
// TOMLStringValue accepts, or something other than a comment follows the closing bracket.
// Whitespace, commas and comments between elements are skipped without checking where the
// commas stand: Cargo parses the file, and this reads only what it accepted. No element spans
// lines, so a caller reading line by line may instead pass each later line of an open array
// behind a "[" of its own and collect the strings (Cargo workspace members,
// internal/flavor/cargo_manifest.go).
func TOMLStringArray(text string) (items []string, closed, ok bool) {
	rest, found := strings.CutPrefix(text, "[")
	if !found {
		return nil, false, false
	}
	// Every pass consumes at least one byte, so len(text) passes read the whole text.
	for range len(text) {
		rest = skipTOMLArraySeparators(rest)
		if rest == "" {
			return items, false, true
		}
		if rest[0] == ']' {
			trailer := strings.TrimSpace(rest[1:])
			return items, true, trailer == "" || strings.HasPrefix(trailer, "#")
		}
		item, after, good := cutTOMLString(rest)
		if !good || (after != "" && !strings.ContainsRune(" \t\r\n,]#", rune(after[0]))) {
			return nil, false, false
		}
		items = append(items, item)
		rest = after
	}
	return nil, false, false
}

// skipTOMLArraySeparators drops the whitespace, commas and comments that open rest.
func skipTOMLArraySeparators(rest string) string {
	for range len(rest) + 1 {
		rest = strings.TrimLeft(rest, " \t\r\n,")
		if !strings.HasPrefix(rest, "#") {
			return rest
		}
		_, rest, _ = strings.Cut(rest, "\n")
	}
	return rest
}

// cutTOMLString cuts the single-line basic ("...") or literal ('...') string that opens value
// and returns its text and what follows it. A basic string holding an escape sequence, which is
// not decoded, and a string running past the end of its line are refused.
func cutTOMLString(value string) (text, rest string, ok bool) {
	if value == "" || (value[0] != '"' && value[0] != '\'') {
		return "", "", false
	}
	quote := value[:1]
	text, rest, closed := strings.Cut(value[1:], quote)
	if !closed || strings.Contains(text, "\n") || (quote == `"` && strings.Contains(text, `\`)) {
		return "", "", false
	}
	return text, rest, true
}
