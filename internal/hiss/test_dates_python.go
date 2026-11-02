// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/hiss/pyliteral"
)

type pythonDateScanner struct {
	src                                      sourceFile
	rep                                      *ScanReport
	text                                     string
	line, lineStart, depth                   int
	annotationLine                           int
	value                                    fixtureDateValue
	valueLine, valueStart, valueEnd, nesting int
}

func scanPythonFixtureDates(src sourceFile, rep *ScanReport) {
	if !isPythonTest(src.rel) {
		return
	}
	s := pythonDateScanner{src: src, rep: rep, text: string(src.data), line: 1}
	for index := 0; index < len(s.text); {
		index = s.step(index)
	}
	s.flush()
}

func (s *pythonDateScanner) step(index int) int {
	switch {
	case s.text[index] == '#':
		return s.comment(index)
	case pyliteral.At(s.text, index):
		return s.literal(index)
	case s.text[index] == '\n':
		s.line++
		s.lineStart = index + 1
	case isIdentByte(s.text[index]):
		for end := index + 1; end < len(s.text); end++ {
			if !isIdentByte(s.text[end]) {
				return end
			}
		}
		return len(s.text)
	case s.text[index] == '(':
		s.depth++
	case s.text[index] == ')':
		s.depth--
	}
	return index + 1
}

func (s *pythonDateScanner) comment(index int) int {
	end := len(s.text)
	if offset := strings.IndexByte(s.text[index:], '\n'); offset >= 0 {
		end = index + offset
	}
	// A trailing comment cannot exempt an earlier literal or the following line.
	if strings.TrimSpace(s.text[s.lineStart:index]) == "" && testDateAllowed(s.text[index:end]) {
		s.annotationLine = s.line + 1
	}
	return end
}

func (s *pythonDateScanner) literal(index int) int {
	decoded, end, ok := s.decodeLiteral(index)
	if !ok {
		return end
	}
	allowed := s.annotationLine == s.line
	s.annotationLine = 0 // One annotation applies to one literal, even on a shared line.
	value := newFixtureDateValue(decoded, allowed)
	if s.valueEnd == 0 || !pythonDateJoin(s.text[s.valueEnd:index], s.nesting) {
		s.flush()
		s.value = value
		s.valueLine = s.line
		s.valueStart = index
	} else {
		s.value = s.value.add(value)
	}
	s.valueEnd, s.nesting = end, s.depth
	s.line += strings.Count(s.text[index:end], "\n")
	if offset := strings.LastIndexByte(s.text[index:end], '\n'); offset >= 0 {
		s.lineStart = index + offset + 1
	}
	return end
}

func (s *pythonDateScanner) decodeLiteral(index int) (string, int, bool) {
	decoded, end, err := pyliteral.Parse(s.text, index)
	if err == nil {
		return decoded, end, true
	}
	end, err = pyliteral.Skip(s.text, index)
	if err != nil {
		recordViolation(s.rep, "HISS-15", s.src.rel, s.line, "", "test fixture string cannot be decoded: "+err.Error())
		return "", len(s.text), false
	}
	return decodeTolerantPythonLiteral(s.text[index:end]), end, true
}

var pythonNamedRunes = map[string]rune{
	"DIGIT ZERO": '0', "DIGIT ONE": '1', "DIGIT TWO": '2', "DIGIT THREE": '3', "DIGIT FOUR": '4',
	"DIGIT FIVE": '5', "DIGIT SIX": '6', "DIGIT SEVEN": '7', "DIGIT EIGHT": '8', "DIGIT NINE": '9',
	"HYPHEN-MINUS": '-', "LINE FEED": '\n', "LF": '\n', "NEW LINE": '\n', "END OF LINE": '\n',
}

func decodeTolerantPythonLiteral(literal string) string {
	quote := strings.IndexAny(literal, "'\"")
	if quote < 0 {
		return literal
	}
	prefix := strings.ToLower(literal[:quote])
	width := 1
	if strings.HasPrefix(literal[quote:], `"""`) || strings.HasPrefix(literal[quote:], `'''`) {
		width = 3
	}
	body := literal[quote+width : len(literal)-width]
	raw := strings.Contains(prefix, "r")
	fstring := strings.Contains(prefix, "f")
	if raw && !fstring {
		return body
	}
	var out strings.Builder
	for index := 0; index < len(body); {
		index = appendTolerantBodyStep(&out, body, index, raw, fstring)
	}
	return out.String()
}

func appendTolerantBodyStep(out *strings.Builder, body string, index int, raw, fstring bool) int {
	if !raw && body[index] == '\\' {
		return index + 1 + appendTolerantEscape(out, body[index+1:])
	}
	if fstring && (body[index] == '{' || body[index] == '}') {
		return appendTolerantFBrace(out, body, index)
	}
	out.WriteByte(body[index])
	return index + 1
}

func appendTolerantFBrace(out *strings.Builder, body string, index int) int {
	if index+1 < len(body) && body[index+1] == body[index] {
		out.WriteByte(body[index])
		return index + 2
	}
	if body[index] == '{' {
		if end := findTolerantFExpressionEnd(body, index+1); end >= 0 {
			out.WriteString("{value}")
			return end + 1
		}
	}
	out.WriteByte(body[index])
	return index + 1
}

func findTolerantFExpressionEnd(body string, start int) int {
	depth := 0
	for index := start; index < len(body); index++ {
		switch body[index] {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return index
			}
			depth--
		}
	}
	return -1
}

func appendTolerantEscape(out *strings.Builder, value string) int {
	if value == "" {
		out.WriteByte('\\')
		return 0
	}
	if consumed, err := pyliteral.AppendEscape(out, value); err == nil {
		return consumed
	}
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
	out.WriteByte('\\')
	out.WriteByte(value[0])
	return 1
}

func consumeSurrogateOrHighOctal(value string) int {
	digits := 0
	switch value[0] {
	case 'u':
		digits = 4
	case 'U':
		digits = 8
	case '4', '5', '6', '7':
		if len(value) >= 3 && isOctalByte(value[1]) && isOctalByte(value[2]) {
			return 3
		}
	}
	if digits > 0 && len(value) >= digits+1 {
		if number, err := strconv.ParseUint(value[1:digits+1], 16, 32); err == nil && number <= utf8.MaxRune {
			return digits + 1
		}
	}
	return 0
}

func isOctalByte(b byte) bool {
	return b >= '0' && b <= '7'
}

// Join only static adjacent literals or additions, never arguments or identifiers.
func pythonDateJoin(gap string, nesting int) bool {
	if strings.Contains(gap, "\n") && nesting == 0 && !strings.Contains(gap, "\\\n") {
		return false
	}
	stripper := literalStripper{syn: pythonSyntax}
	for _, line := range strings.Split(gap, "\n") {
		if strings.Trim(stripper.strip(line), " \t\r+()\\") != "" {
			return false
		}
	}
	return true
}

func (s *pythonDateScanner) flush() {
	if s.valueEnd > 0 && !s.value.allowed {
		checkFixtureDateText(s.value, s.text[s.valueStart:s.valueEnd], s.src.rel, s.valueLine, s.rep)
	}
	s.valueEnd = 0
}
