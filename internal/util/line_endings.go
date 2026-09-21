package util

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// NormalizeLineEndings converts CRLF to LF and reports whether the source used CRLF.
// Callers can run deterministic text transforms on the result and restore the source
// convention with RestoreLineEndings.
func NormalizeLineEndings(content string) (normalized string, crlf bool) {
	if !strings.Contains(content, "\r\n") {
		return content, false
	}
	return strings.ReplaceAll(content, "\r\n", "\n"), true
}

// RestoreLineEndings converts LF to CRLF when NormalizeLineEndings observed CRLF.
func RestoreLineEndings(content string, crlf bool) string {
	if !crlf {
		return content
	}
	return strings.ReplaceAll(content, "\n", "\r\n")
}

// NormalizeLineEndingsStrict converts one consistent LF or CRLF text style to LF.
// Mixed endings and lone carriage returns are rejected instead of silently repaired.
func NormalizeLineEndingsStrict(content string) (normalized string, crlf bool, err error) {
	withoutCRLF := strings.ReplaceAll(content, "\r\n", "")
	hasCRLF := len(withoutCRLF) != len(content)
	if strings.Contains(withoutCRLF, "\r") {
		return "", false, fmt.Errorf("text contains a lone carriage return")
	}
	if hasCRLF && strings.Contains(withoutCRLF, "\n") {
		return "", false, fmt.Errorf("text contains mixed LF and CRLF line endings")
	}
	if !hasCRLF {
		return content, false, nil
	}
	return strings.ReplaceAll(content, "\r\n", "\n"), true, nil
}

// CanonicalTextEquivalent compares UTF-8 text while allowing one consistent checkout
// line-ending style. Content changes and mixed line endings remain distinguishable.
func CanonicalTextEquivalent(actual, expected []byte) (bool, error) {
	if !utf8.Valid(actual) || !utf8.Valid(expected) {
		return false, fmt.Errorf("canonical text is not valid UTF-8")
	}
	actualLF, _, err := NormalizeLineEndingsStrict(string(actual))
	if err != nil {
		return false, err
	}
	expectedLF, _, err := NormalizeLineEndingsStrict(string(expected))
	if err != nil {
		return false, fmt.Errorf("canonical text is invalid: %w", err)
	}
	return bytes.Equal([]byte(actualLF), []byte(expectedLF)), nil
}
