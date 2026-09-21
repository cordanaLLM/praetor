package cavemansource

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func extractPython(item discoveredInput, text string) ([]Source, error) {
	text = normalizeNewlines(text)
	starts := textLineStarts(text)
	sources := []Source{}
	for index := 0; index < len(text); {
		next, source, err := scanPythonSource(item, text, starts, index)
		if err != nil {
			return nil, err
		}
		if source != nil {
			sources = append(sources, *source)
		}
		index = next
	}
	return sources, nil
}

func scanPythonSource(item discoveredInput, text string, starts []int, index int) (int, *Source, error) {
	if text[index] == '#' {
		return skipToNextLine(text, index), nil, nil
	}
	if text[index] == '\'' || text[index] == '"' {
		_, end, err := parsePythonString(text, index)
		if err != nil {
			return 0, nil, fmt.Errorf("caveman source %s line %d: %w", item.path, lineNumber(starts, index), err)
		}
		return end, nil, nil
	}
	if !pythonIdentifierStart(text[index]) {
		return index + 1, nil, nil
	}
	end := scanPythonIdentifier(text, index)
	if text[index:end] != "print" || pythonAttributeCall(text, index) {
		return end, nil, nil
	}
	open := skipPythonSpace(text, end)
	if open >= len(text) || text[open] != '(' {
		return end, nil, nil
	}
	value, callEnd, err := parsePythonPrint(text, open)
	line := lineNumber(starts, index)
	if err != nil {
		return 0, nil, fmt.Errorf("caveman source %s line %d: print output unverified: %w", item.path, line, err)
	}
	source := sourceFrom(item, value, fmt.Sprintf("print:%d", line), line)
	return callEnd, &source, nil
}

func parsePythonPrint(text string, open int) (string, int, error) {
	start := skipPythonSpace(text, open+1)
	value, end, err := parsePythonString(text, start)
	if err != nil {
		return "", 0, fmt.Errorf("static string literal required: %w", err)
	}
	end = skipPythonSpace(text, end)
	if end >= len(text) || text[end] != ')' {
		return "", 0, errors.New("computed, concatenated, multiple, or keyword arguments are unsupported")
	}
	return value, end + 1, nil
}

func parsePythonString(text string, start int) (string, int, error) {
	prefix, index, err := scanPythonStringPrefix(text, start)
	if err != nil {
		return "", 0, err
	}
	quote, width, err := pythonStringDelimiter(text, index)
	if err != nil {
		return "", 0, err
	}
	bodyStart := index + width
	bodyEnd, err := findPythonStringEnd(text, bodyStart, quote, width)
	if err != nil {
		return "", 0, err
	}
	body := text[bodyStart:bodyEnd]
	if strings.Contains(prefix, "r") {
		return body, bodyEnd + width, nil
	}
	decoded, err := decodePythonEscapes(body)
	return decoded, bodyEnd + width, err
}

func scanPythonStringPrefix(text string, start int) (string, int, error) {
	index := start
	for index < len(text) && index-start < 3 && pythonPrefix(text[index]) {
		index++
	}
	prefix := strings.ToLower(text[start:index])
	if strings.ContainsAny(prefix, "fb") {
		return "", 0, errors.New("formatted and byte strings are unsupported")
	}
	if prefix != "" && prefix != "r" && prefix != "u" {
		return "", 0, errors.New("unsupported Python string prefix")
	}
	return prefix, index, nil
}

func pythonStringDelimiter(text string, index int) (byte, int, error) {
	if index >= len(text) || (text[index] != '\'' && text[index] != '"') {
		return 0, 0, errors.New("expected Python string literal")
	}
	quote := text[index]
	if index+2 < len(text) && text[index+1] == quote && text[index+2] == quote {
		return quote, 3, nil
	}
	return quote, 1, nil
}

func findPythonStringEnd(text string, start int, quote byte, width int) (int, error) {
	for index := start; index < len(text); index++ {
		if text[index] == '\\' {
			index++
			continue
		}
		if text[index] != quote {
			continue
		}
		if width == 1 || index+2 < len(text) && text[index+1] == quote && text[index+2] == quote {
			return index, nil
		}
	}
	return 0, errors.New("unterminated Python string literal")
}

func decodePythonEscapes(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", errors.New("trailing Python escape")
		}
		index++
		consumed, err := appendPythonEscape(&out, value[index:])
		if err != nil {
			return "", err
		}
		index += consumed - 1
	}
	return out.String(), nil
}

func appendPythonEscape(out *strings.Builder, value string) (int, error) {
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"'}
	if decoded, ok := simple[value[0]]; ok {
		out.WriteByte(decoded)
		return 1, nil
	}
	if value[0] == '\n' {
		return 1, nil
	}
	if value[0] == 'x' {
		return appendPythonHex(out, value, 2)
	}
	if value[0] == 'u' {
		return appendPythonHex(out, value, 4)
	}
	if value[0] == 'U' {
		return appendPythonHex(out, value, 8)
	}
	if value[0] == 'N' {
		return 0, errors.New("named Python Unicode escapes are unsupported")
	}
	if value[0] >= '0' && value[0] <= '7' {
		return appendPythonOctal(out, value)
	}
	out.WriteByte('\\')
	out.WriteByte(value[0])
	return 1, nil
}

func appendPythonHex(out *strings.Builder, value string, digits int) (int, error) {
	if len(value) < digits+1 {
		return 0, errors.New("short Python hexadecimal escape")
	}
	number, err := strconv.ParseUint(value[1:digits+1], 16, 32)
	if err != nil || !utf8.ValidRune(rune(number)) {
		return 0, errors.New("invalid Python hexadecimal escape")
	}
	out.WriteRune(rune(number))
	return digits + 1, nil
}

func appendPythonOctal(out *strings.Builder, value string) (int, error) {
	length := 1
	for length < len(value) && length < 3 && value[length] >= '0' && value[length] <= '7' {
		length++
	}
	number, err := strconv.ParseUint(value[:length], 8, 8)
	if err != nil {
		return 0, errors.New("invalid Python octal escape")
	}
	out.WriteByte(byte(number))
	return length, nil
}

func pythonIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func scanPythonIdentifier(text string, start int) int {
	end := start + 1
	for end < len(text) && (pythonIdentifierStart(text[end]) || text[end] >= '0' && text[end] <= '9') {
		end++
	}
	return end
}

func pythonAttributeCall(text string, start int) bool {
	for index := start - 1; index >= 0 && (text[index] == ' ' || text[index] == '\t'); index-- {
		return text[index] == '.'
	}
	return false
}

func pythonPrefix(value byte) bool {
	return strings.ContainsRune("rRuUbBfF", rune(value))
}

func skipPythonSpace(text string, start int) int {
	for start < len(text) && strings.ContainsRune(" \t\r\n", rune(text[start])) {
		start++
	}
	return start
}

func skipToNextLine(text string, start int) int {
	if offset := strings.IndexByte(text[start:], '\n'); offset >= 0 {
		return start + offset + 1
	}
	return len(text)
}

func textLineStarts(text string) []int {
	starts := []int{0}
	for index := range text {
		if text[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func lineNumber(starts []int, offset int) int {
	return sort.Search(len(starts), func(index int) bool { return starts[index] > offset })
}
