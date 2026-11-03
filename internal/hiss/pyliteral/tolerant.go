// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pyliteral

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseTolerant decodes a text literal, recovering named escapes, lone surrogates,
// and three-digit octals above \377. Unknown names and invalid runes become U+FFFD.
// Literal boundaries and formatted expressions follow Parse.
func ParseTolerant(text string, start int) (string, int, error) {
	d := pythonLiteralDecoder{tolerant: true}
	return d.parse(text, start)
}

// Expressions returns the byte spans of replacement expressions inside a Python f-string at start.
func Expressions(text string, start int) [][2]int {
	d := pythonLiteralDecoder{tolerant: true}
	if _, _, err := d.parse(text, start); err != nil {
		return nil
	}
	return d.spans
}

var pythonNamedRunes = map[string]rune{
	"DIGIT ZERO": '0', "DIGIT ONE": '1', "DIGIT TWO": '2', "DIGIT THREE": '3', "DIGIT FOUR": '4',
	"DIGIT FIVE": '5', "DIGIT SIX": '6', "DIGIT SEVEN": '7', "DIGIT EIGHT": '8', "DIGIT NINE": '9',
	"HYPHEN-MINUS": '-', "LINE FEED": '\n', "LF": '\n', "NEW LINE": '\n', "END OF LINE": '\n',
}

// appendTolerantEscape preserves or escapes unhandled Python escape patterns.
func appendTolerantEscape(out *strings.Builder, value string) int {
	if strings.HasPrefix(value, "N{") {
		if end := strings.IndexByte(value[2:], '}'); end > 0 && !strings.ContainsAny(value[2:2+end], "{\\\r\n") {
			r, ok := pythonNamedRunes[strings.ToUpper(value[2:2+end])]
			if !ok {
				r = utf8.RuneError
			}
			out.WriteRune(r)
			return end + 3
		}
	}
	if consumed := consumeSurrogateOrHighOctal(value); consumed > 0 {
		out.WriteRune(utf8.RuneError)
		return consumed
	}
	return 0
}

// consumeSurrogateOrHighOctal parses Unicode surrogate pairs or high octal values.
func consumeSurrogateOrHighOctal(value string) int {
	switch value[0] {
	case 'u':
		return consumeSurrogate(value, 4)
	case 'U':
		return consumeSurrogate(value, 8)
	case '4', '5', '6', '7':
		if len(value) >= 3 && isOctalByte(value[1]) && isOctalByte(value[2]) {
			return 3
		}
	}
	return 0
}

// consumeSurrogate checks and skips Unicode surrogate codepoints.
func consumeSurrogate(value string, digits int) int {
	if len(value) >= digits+1 {
		if number, err := strconv.ParseUint(value[1:digits+1], 16, 32); err == nil && number >= 0xD800 && number <= 0xDFFF {
			return digits + 1
		}
	}
	return 0
}

// isOctalByte reports whether a byte is an octal digit.
func isOctalByte(b byte) bool {
	return b >= '0' && b <= '7'
}
