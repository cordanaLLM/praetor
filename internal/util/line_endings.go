package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// CanonicalTextDigest returns the lowercase hexadecimal SHA-256 of data's LF text and whether
// data used CRLF. It is the digest form of CanonicalTextEquivalent's rule: one consistent
// checkout line-ending style is allowed, so an LF text and its CRLF checkout share one digest,
// while mixed endings and lone carriage returns are an error, never repaired into a match.
// Digests of recorded texts compare with it (LookupCanonicalText), and crlf lets a caller that
// rewrites such a text keep the file's own style (RestoreLineEndings).
func CanonicalTextDigest(data []byte) (digest string, crlf bool, err error) {
	normalized, crlf, err := NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:]), crlf, nil
}

// LookupCanonicalText returns the value recorded maps data's CanonicalTextDigest to, whether
// there is one, and whether data used CRLF. It is the one lookup for a set of recorded texts
// keyed by the SHA-256 of their LF text, such as the earlier texts Praetor once wrote at a path:
// an LF text and its CRLF checkout find the same entry, while an edit, mixed endings or a lone
// carriage return find none (known is false, never an error to repair).
func LookupCanonicalText(data []byte, recorded map[string]string) (value string, known, crlf bool) {
	digest, crlf, err := CanonicalTextDigest(data)
	if err != nil {
		return "", false, false
	}
	value, known = recorded[digest]
	return value, known, crlf
}
