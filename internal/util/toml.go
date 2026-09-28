// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// TOMLTableName normalizes a TOML table header line to its name with the spaces TOML permits
// removed and a trailing comment dropped: "[ extend ]" is "extend" and "[[ rules ]]  # x" is
// "[rules]", an array-of-tables header keeping one bracket pair. It reads a line's shape and is
// not a parser: the module carries no TOML library, and the callers (the gitleaks configuration
// check and the Cargo.toml edition read in internal/flavor, and the REUSE.toml read of
// praetorctl audit) need only the header and single-line keys (TOMLKeyValue).
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

// TOMLStringValue returns the text of the single-line string value holds, value being what
// TOMLKeyValue returns after the equals sign: a basic ("...") or literal ('...') string followed
// by nothing or a comment. Anything else is refused rather than guessed at: a number, a boolean,
// an inline table, a multi-line string, and a basic string holding an escape sequence, which is
// not decoded.
func TOMLStringValue(value string) (string, bool) {
	if value == "" || (value[0] != '"' && value[0] != '\'') {
		return "", false
	}
	quote := value[:1]
	text, rest, closed := strings.Cut(value[1:], quote)
	rest = strings.TrimSpace(rest)
	if !closed || (rest != "" && !strings.HasPrefix(rest, "#")) || (quote == `"` && strings.Contains(text, `\`)) {
		return "", false
	}
	return text, true
}
