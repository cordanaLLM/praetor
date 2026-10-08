// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

// TOMLKeyPath tokenizes the TOML 1.0 key that opens text (the key ABNF of toml.io/en/v1.0.0): one
// or more segments joined by dots, each a bare key ([A-Za-z0-9_-]+), a basic string without any
// backslash or a literal string. Spaces and tabs are allowed only between tokens, never inside a
// bare key or a quoted one. It returns the decoded segments, so "annotations" and annotations
// are the same segment, and the text after the key with its leading spaces and tabs dropped. A
// text that does not open with such a key is an error, never a guess: a segment it cannot
// tokenize exactly (an escape sequence, an unclosed quote, an empty bare key) fails closed.
// TOMLTableName and TOMLKeyValue read a line's shape and strip spaces inside quotes; readers
// that must agree with a TOML parser on which key a line names, such as the REUSE.toml read of
// internal/supplychain, use this one.
func TOMLKeyPath(text string) (segments []string, rest string, err error) {
	const blanks = " \t"
	rest = text
	// Every pass consumes at least one byte of a segment, so len(text)+1 passes read the key.
	for range len(text) + 1 {
		segment, after, cutErr := cutTOMLKeySegment(strings.TrimLeft(rest, blanks))
		if cutErr != nil {
			return nil, "", cutErr
		}
		segments = append(segments, segment)
		rest = strings.TrimLeft(after, blanks)
		next, dotted := strings.CutPrefix(rest, ".")
		if !dotted {
			return segments, rest, nil
		}
		rest = next
	}
	return nil, "", errTOMLKeyBound
}

var errTOMLKeyBound = errors.New("key longer than the read bound")

// cutTOMLKeySegment cuts the bare or quoted key segment that opens text.
func cutTOMLKeySegment(text string) (segment, rest string, err error) {
	if text == "" {
		return "", "", errors.New("a key segment is missing")
	}
	if quote := text[0]; quote == '"' || quote == '\'' {
		body, after, closed := strings.Cut(text[1:], string(quote))
		switch {
		case !closed:
			return "", "", errors.New("a quoted key is not closed")
		case strings.ContainsAny(body, "\\\n"):
			return "", "", errors.New("a quoted key holds a backslash or a line break")
		}
		return body, after, nil
	}
	end := strings.IndexFunc(text, func(r rune) bool { return !isTOMLBareKeyRune(r) })
	if end < 0 {
		end = len(text)
	}
	if end == 0 {
		return "", "", fmt.Errorf("%q is not a bare key character (A-Z a-z 0-9 _ -)", text[:1])
	}
	return text[:end], text[end:], nil
}

// isTOMLBareKeyRune reports whether r may appear in a bare key.
func isTOMLBareKeyRune(r rune) bool {
	return r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
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
// TOMLKeyValue returns after the equals sign: a basic ("...") string, its escape sequences
// decoded (decodeTOMLEscape), or a literal ('...') one, followed by nothing or a comment.
// Anything else is refused rather than guessed at: a number, a boolean, an inline table, a
// multi-line string, and a basic string holding an escape sequence TOML does not define.
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
// and returns its text, a basic string's escape sequences decoded, and what follows it. A string
// running past the end of its line, and a basic string holding an escape sequence TOML does not
// define, are refused.
func cutTOMLString(value string) (text, rest string, ok bool) {
	switch {
	case strings.HasPrefix(value, `"`):
		return cutTOMLBasicString(value[1:])
	case !strings.HasPrefix(value, "'"):
		return "", "", false
	}
	text, rest, closed := strings.Cut(value[1:], "'")
	if !closed || strings.Contains(text, "\n") {
		return "", "", false
	}
	return text, rest, true
}

// cutTOMLBasicString reads body, what follows the opening quote of a single-line basic string,
// to its closing quote, and returns the string's text with every escape sequence decoded and what
// follows the quote.
func cutTOMLBasicString(body string) (text, rest string, ok bool) {
	var decoded strings.Builder
	// Every pass consumes at least one byte, so len(body) passes read the whole body.
	for index := 0; index < len(body); {
		switch body[index] {
		case '"':
			return decoded.String(), body[index+1:], true
		case '\n':
			return "", "", false
		case '\\':
			char, width, good := decodeTOMLEscape(body[index:])
			if !good {
				return "", "", false
			}
			decoded.WriteString(char)
			index += width
		default:
			decoded.WriteByte(body[index])
			index++
		}
	}
	return "", "", false
}

// tomlEscapes are the escape sequences of one letter after the backslash a TOML basic string may
// hold, with the text each stands for.
var tomlEscapes = map[byte]string{'b': "\b", 't': "\t", 'n': "\n", 'f': "\f", 'r': "\r", 'e': "\x1b", '"': `"`, '\\': `\`}

// tomlCodeEscapes are the escape letters a fixed number of hexadecimal digits follows, naming a
// Unicode scalar value: \xHH, \uHHHH and \UHHHHHHHH.
var tomlCodeEscapes = map[byte]int{'x': 2, 'u': 4, 'U': 8}

// decodeTOMLEscape decodes the escape sequence that opens rest, a backslash first, and returns its
// text and the bytes it spans. It takes the escapes TOML 1.0 defines and the \e and \xHH that
// tomlkit 0.15, the TOML library of the reuse tool, decodes too (tomlEscapes, tomlCodeEscapes),
// and refuses any other, a code that is no Unicode scalar value included.
func decodeTOMLEscape(rest string) (text string, width int, ok bool) {
	if len(rest) < 2 {
		return "", 0, false
	}
	if decoded, simple := tomlEscapes[rest[1]]; simple {
		return decoded, 2, true
	}
	digits := tomlCodeEscapes[rest[1]]
	if digits == 0 || len(rest) < 2+digits {
		return "", 0, false
	}
	code, err := strconv.ParseUint(rest[2:2+digits], 16, 32)
	if err != nil || code > unicode.MaxRune {
		return "", 0, false
	}
	char := rune(code)
	if !utf8.ValidRune(char) {
		return "", 0, false
	}
	return string(char), 2 + digits, true
}

// TOMLValueScan follows the extent of one TOML value across lines without decoding it, so a
// reader of a file's shape can step over a value it does not read: it tracks basic and literal
// strings with their escapes, multi-line strings, and the nesting of arrays and inline tables,
// and drops comments. The zero value is ready for a value's first line. The REUSE.toml read of
// internal/supplychain steps over the keys it does not keep with it.
type TOMLValueScan struct {
	// depth counts the arrays and inline tables open.
	depth int
	// multiline is the delimiter of the multi-line string open, `"""` or "'''", or "".
	multiline string
}

// Feed reads one more line of the value, the first being what TOMLKeyValue returns after the
// equals sign, and reports whether the value ends on it. ok is false for a line the scan cannot
// follow: a single-line string left open, or a closing bracket or brace with nothing open.
func (s *TOMLValueScan) Feed(line string) (done, ok bool) {
	// Every token spans at least one byte, so len(line) passes read the whole line.
	for index := 0; index < len(line); {
		width, comment, good := s.next(line[index:])
		if !good {
			return false, false
		}
		if comment {
			break
		}
		index += width
	}
	return s.depth == 0 && s.multiline == "", true
}

// next reads the token rest opens and returns the bytes it spans; comment is a comment, which
// runs to the end of the line. rest is not empty.
func (s *TOMLValueScan) next(rest string) (width int, comment, ok bool) {
	if s.multiline != "" {
		return s.inMultiline(rest), false, true
	}
	switch rest[0] {
	case '#':
		return 0, true, true
	case '[', '{':
		s.depth++
	case ']', '}':
		if s.depth == 0 {
			return 0, false, false
		}
		s.depth--
	case '"', '\'':
		width, ok = s.openString(rest)
		return width, false, ok
	}
	return 1, false, true
}

// openString reads the string rest opens: a single-line string to its closing quote, or the
// opening delimiter of a multi-line one, which stays open. A single-line string the line does not
// close is refused.
func (s *TOMLValueScan) openString(rest string) (int, bool) {
	quote := rest[0]
	if delimiter := strings.Repeat(rest[:1], 3); strings.HasPrefix(rest, delimiter) {
		s.multiline = delimiter
		return len(delimiter), true
	}
	for index := 1; index < len(rest); index++ {
		switch {
		case quote == '"' && rest[index] == '\\':
			index++
		case rest[index] == quote:
			return index + 1, true
		}
	}
	return 0, false
}

// inMultiline reads one token of the multi-line string open: an escape of a basic one, the run
// of quotes that closes it (up to two quotes of the text may precede the delimiter), or one byte
// of its text.
func (s *TOMLValueScan) inMultiline(rest string) int {
	switch {
	case s.multiline == `"""` && rest[0] == '\\':
		return min(2, len(rest))
	case strings.HasPrefix(rest, s.multiline):
		run := len(rest) - len(strings.TrimLeft(rest, s.multiline[:1]))
		s.multiline = ""
		return run
	}
	return 1
}
