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

// CheckoutTextDigest returns the lowercase hexadecimal SHA-256 one text shares with each
// checkout of it, for an input a digest pins across platforms, such as an archetype source or
// a policy source. A text in one consistent line-ending style hashes as its LF form
// (CanonicalTextDigest): an LF text keeps the digest of its bytes, and its CRLF checkout
// (core.eol=crlf under "* text=auto", git's default on Windows) hashes alike. A text with
// mixed endings or a lone carriage return hashes byte for byte, and strict says why, so a
// caller reporting a mismatch can say that no line ending was folded (ByteExactNote).
func CheckoutTextDigest(data []byte) (digest, strict string) {
	digest, _, err := CanonicalTextDigest(data)
	if err == nil {
		return digest, ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), err.Error()
}

// CheckoutTextEqual compares a checked-out text with the text it must hold under
// CheckoutTextDigest's rule. When both are UTF-8 in one consistent line-ending style each, they
// compare as LF text (CanonicalTextEquivalent), so a CRLF checkout equals its LF source and an
// edit still differs. Otherwise they compare byte for byte, and strict says why.
func CheckoutTextEqual(actual, expected []byte) (equal bool, strict string) {
	equivalent, err := CanonicalTextEquivalent(actual, expected)
	if err != nil {
		return bytes.Equal(actual, expected), err.Error()
	}
	return equivalent, ""
}

// ByteExactNote is the clause a mismatch report appends for the strict reason
// CheckoutTextDigest or CheckoutTextEqual returned: empty when line endings were folded, and
// otherwise a parenthesised statement that the bytes were compared exactly and why.
func ByteExactNote(strict string) string {
	if strict == "" {
		return ""
	}
	return " (compared byte for byte: " + strict + ")"
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
