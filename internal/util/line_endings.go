package util

import "strings"

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
