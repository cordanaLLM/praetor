package util

import (
	"strings"
	"testing"
)

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

func TestNormalizeLineEndingsStrictAcceptsConsistentStyles(t *testing.T) {
	for name, input := range map[string]string{
		"LF":   "one\ntwo\n",
		"CRLF": "one\r\ntwo\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			normalized, crlf, err := NormalizeLineEndingsStrict(input)
			if err != nil || normalized != "one\ntwo\n" || crlf != (name == "CRLF") {
				t.Fatalf("strict normalize: normalized=%q crlf=%v err=%v", normalized, crlf, err)
			}
		})
	}
}

func TestNormalizeLineEndingsStrictRejectsMixedAndLoneCR(t *testing.T) {
	for name, input := range map[string]string{
		"mixed":   "one\r\ntwo\n",
		"lone CR": "one\rtwo",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := NormalizeLineEndingsStrict(input); err == nil {
				t.Fatal("inconsistent line endings accepted")
			}
		})
	}
}

func TestCanonicalTextEquivalentAllowsOnlyConsistentEOLDifference(t *testing.T) {
	canonical := []byte("one\ntwo\n")
	for _, actual := range [][]byte{canonical, []byte("one\r\ntwo\r\n")} {
		equal, err := CanonicalTextEquivalent(actual, canonical)
		if err != nil || !equal {
			t.Fatalf("canonical text rejected: equal=%v err=%v", equal, err)
		}
	}
	equal, err := CanonicalTextEquivalent([]byte("changed\r\n"), canonical)
	if err != nil || equal {
		t.Fatalf("content drift result: equal=%v err=%v", equal, err)
	}
	if _, err := CanonicalTextEquivalent([]byte("one\r\ntwo\n"), canonical); err == nil ||
		!strings.Contains(err.Error(), "mixed") {
		t.Fatalf("mixed line endings were not rejected: %v", err)
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
