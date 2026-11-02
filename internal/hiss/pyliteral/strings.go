// Package pyliteral decodes Python string syntax for source scanners.
package pyliteral

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Parse decodes one Python text literal at start; formatted expressions become {value}.
func Parse(text string, start int) (string, int, error) {
	return (pythonLiteralDecoder{}).parse(text, start)
}

type pythonLiteralDecoder struct {
	tolerant bool
}

func (d pythonLiteralDecoder) parse(text string, start int) (string, int, error) {
	if start < 0 || start >= len(text) {
		return "", 0, errors.New("string offset outside Python source")
	}
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
		if strings.Contains(prefix, "f") {
			value, _, decodeErr := d.decodePythonFString(body, true)
			return value, bodyEnd + width, decodeErr
		}
		return body, bodyEnd + width, nil
	}
	if strings.Contains(prefix, "f") {
		value, _, decodeErr := d.decodePythonFString(body, false)
		return value, bodyEnd + width, decodeErr
	}
	decoded, err := d.decodePythonEscapes(body)
	return decoded, bodyEnd + width, err
}

// supportedPythonStringPrefixes are the lower-cased prefixes whose literal decodes to text.
var supportedPythonStringPrefixes = map[string]bool{"": true, "r": true, "u": true, "f": true, "fr": true, "rf": true}

func scanPythonStringPrefix(text string, start int) (string, int, error) {
	index := start
	for index < len(text) && index-start < 3 && pythonPrefix(text[index]) {
		index++
	}
	prefix := strings.ToLower(text[start:index])
	if strings.Contains(prefix, "b") {
		return "", 0, errors.New("byte strings are unsupported")
	}
	if !supportedPythonStringPrefixes[prefix] {
		return "", 0, errors.New("unsupported Python string prefix")
	}
	return prefix, index, nil
}

func (d pythonLiteralDecoder) decodePythonFString(body string, raw bool) (string, int, error) {
	var out strings.Builder
	for index := 0; index < len(body); {
		next, err := d.appendPythonFStringPart(&out, body, index, raw)
		if err != nil {
			return "", 0, err
		}
		index = next
	}
	return out.String(), len(body), nil
}

func (d pythonLiteralDecoder) appendPythonFStringPart(out *strings.Builder, body string, index int, raw bool) (int, error) {
	switch body[index] {
	case '{':
		return appendPythonFStringOpen(out, body, index)
	case '}':
		if index+1 < len(body) && body[index+1] == '}' {
			out.WriteByte('}')
			return index + 2, nil
		}
		return 0, errors.New("unmatched formatted string brace")
	default:
		return d.appendPythonFStringText(out, body, index, raw)
	}
}

func appendPythonFStringOpen(out *strings.Builder, body string, index int) (int, error) {
	if index+1 < len(body) && body[index+1] == '{' {
		out.WriteByte('{')
		return index + 2, nil
	}
	end, err := findPythonFExpressionEnd(body, index+1)
	if err != nil {
		return 0, err
	}
	out.WriteString("{value}")
	return end + 1, nil
}

func (d pythonLiteralDecoder) appendPythonFStringText(out *strings.Builder, body string, index int, raw bool) (int, error) {
	for index < len(body) && body[index] != '{' && body[index] != '}' {
		next, err := d.appendPythonText(out, body, index, raw)
		if err != nil {
			return 0, err
		}
		index = next
	}
	return index, nil
}

func findPythonFExpressionEnd(body string, start int) (int, error) {
	depth := 0
	for index := start; index < len(body); index++ {
		if body[index] == '{' {
			depth++
			continue
		}
		if body[index] != '}' {
			continue
		}
		if depth == 0 {
			return index, nil
		}
		depth--
	}
	return 0, errors.New("unterminated formatted string expression")
}

// At reports whether text starts a supported Python string at index.
func At(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	if text[index] == '\'' || text[index] == '"' {
		return true
	}
	if !pythonPrefix(text[index]) {
		return false
	}
	_, quote, err := scanPythonStringPrefix(text, index)
	return err == nil && quote < len(text) && (text[quote] == '\'' || text[quote] == '"')
}

// Skip returns the offset after a Python string without decoding its body.
func Skip(text string, start int) (int, error) {
	if start < 0 || start >= len(text) {
		return 0, errors.New("string offset outside Python source")
	}
	_, index, err := scanPythonStringPrefix(text, start)
	if err != nil {
		return 0, err
	}
	quote, width, err := pythonStringDelimiter(text, index)
	if err != nil {
		return 0, err
	}
	end, err := findPythonStringEnd(text, index+width, quote, width)
	if err != nil {
		return 0, err
	}
	return end + width, nil
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

func (d pythonLiteralDecoder) decodePythonEscapes(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); {
		next, err := d.appendPythonText(&out, value, index, false)
		if err != nil {
			return "", err
		}
		index = next
	}
	return out.String(), nil
}

func (d pythonLiteralDecoder) appendPythonText(out *strings.Builder, value string, index int, raw bool) (int, error) {
	if raw || value[index] != '\\' {
		out.WriteByte(value[index])
		return index + 1, nil
	}
	if index+1 >= len(value) {
		return 0, errors.New("trailing Python escape")
	}
	consumed, err := AppendEscape(out, value[index+1:])
	if err != nil && d.tolerant {
		consumed = appendTolerantEscape(out, value[index+1:])
		if consumed > 0 {
			err = nil
		}
	}
	return index + 1 + consumed, err
}

// AppendEscape writes one Python escape body and returns the consumed byte count.
func AppendEscape(out *strings.Builder, value string) (int, error) {
	if value == "" {
		return 0, errors.New("empty Python escape")
	}
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
	var decoded rune
	_, err := fmt.Sscanf(value[1:digits+1], "%x", &decoded)
	if err != nil || !utf8.ValidRune(decoded) {
		return 0, errors.New("invalid Python hexadecimal escape")
	}
	out.WriteRune(decoded)
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

func pythonPrefix(value byte) bool {
	return strings.ContainsRune("rRuUbBfF", rune(value))
}
