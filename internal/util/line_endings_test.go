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
