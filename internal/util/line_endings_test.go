package util

import "testing"

func TestNormalizeLineEndingsPositiveRestoresCRLF(t *testing.T) {
	normalized, crlf := NormalizeLineEndings("one\r\ntwo\r\n")
	if normalized != "one\ntwo\n" || !crlf {
		t.Fatalf("normalize CRLF: normalized=%q crlf=%v", normalized, crlf)
	}
	if restored := RestoreLineEndings(normalized, crlf); restored != "one\r\ntwo\r\n" {
		t.Fatalf("restore CRLF: %q", restored)
	}
}

func TestNormalizeLineEndingsNegativeLeavesLFUnchanged(t *testing.T) {
	const input = "one\ntwo\n"
	normalized, crlf := NormalizeLineEndings(input)
	if normalized != input || crlf {
		t.Fatalf("normalize LF: normalized=%q crlf=%v", normalized, crlf)
	}
	if restored := RestoreLineEndings(normalized, crlf); restored != input {
		t.Fatalf("restore LF: %q", restored)
	}
}

func TestNormalizeLineEndingsBoundaryCanonicalizesMixedInput(t *testing.T) {
	normalized, crlf := NormalizeLineEndings("one\r\ntwo\n")
	if normalized != "one\ntwo\n" || !crlf {
		t.Fatalf("normalize mixed input: normalized=%q crlf=%v", normalized, crlf)
	}
	if restored := RestoreLineEndings(normalized, crlf); restored != "one\r\ntwo\r\n" {
		t.Fatalf("restore mixed input: %q", restored)
	}
}

// TestNormalizeLineEndingsBoundaryKeepsLoneCRAndRoundTrips pins the cases the
// devcontainer verifier relies on: a lone CR is not a line ending and survives,
// empty input reports no conversion, and a uniform document round-trips.
func TestNormalizeLineEndingsBoundaryKeepsLoneCRAndRoundTrips(t *testing.T) {
	for _, input := range []string{"", "a\rb", "no trailing newline"} {
		normalized, crlf := NormalizeLineEndings(input)
		if normalized != input || crlf {
			t.Fatalf("normalize %q: normalized=%q crlf=%v", input, normalized, crlf)
		}
	}
	for _, original := range []string{"a\r\nb\r\n", "a\nb\n", ""} {
		normalized, crlf := NormalizeLineEndings(original)
		if restored := RestoreLineEndings(normalized, crlf); restored != original {
			t.Fatalf("round trip of %q returned %q", original, restored)
		}
	}
}
