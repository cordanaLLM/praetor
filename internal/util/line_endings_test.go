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

func TestCanonicalTextDigestPositiveSharesOneDigestAcrossStyles(t *testing.T) {
	lf, lfCRLF, err := CanonicalTextDigest([]byte("version: 1\nname: x\n"))
	if err != nil || lfCRLF {
		t.Fatalf("LF text: crlf=%v err=%v", lfCRLF, err)
	}
	crlf, crlfCRLF, err := CanonicalTextDigest([]byte("version: 1\r\nname: x\r\n"))
	if err != nil || !crlfCRLF {
		t.Fatalf("CRLF text: crlf=%v err=%v", crlfCRLF, err)
	}
	if lf != crlf {
		t.Fatalf("a CRLF checkout must share the LF digest: %s != %s", crlf, lf)
	}
	if len(lf) != 64 || strings.Trim(lf, "0123456789abcdef") != "" {
		t.Fatalf("digest is not a lowercase hexadecimal SHA-256: %q", lf)
	}
}

func TestCanonicalTextDigestNegativeSeparatesEditsAndRejectsMixedEndings(t *testing.T) {
	original, _, err := CanonicalTextDigest([]byte("version: 1\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	edited, _, err := CanonicalTextDigest([]byte("version: 2\r\n"))
	if err != nil || edited == original {
		t.Fatalf("an edited CRLF text must digest differently: err=%v", err)
	}
	for _, text := range []string{"a\r\nb\n", "a\rb\n"} {
		if digest, _, err := CanonicalTextDigest([]byte(text)); err == nil || digest != "" {
			t.Errorf("%q: mixed or lone carriage returns must be an error, got %q", text, digest)
		}
	}
}

func TestCanonicalTextDigestBoundaryEmptyAndUnterminatedText(t *testing.T) {
	empty, crlf, err := CanonicalTextDigest(nil)
	if err != nil || crlf || empty != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty text: digest=%s crlf=%v err=%v", empty, crlf, err)
	}
	unterminated, _, err := CanonicalTextDigest([]byte("x"))
	if err != nil || unterminated == empty {
		t.Fatalf("a text without a line ending digests its bytes: %s err=%v", unterminated, err)
	}
	terminated, _, err := CanonicalTextDigest([]byte("x\n"))
	if err != nil || terminated == unterminated {
		t.Fatalf("a final line ending is content, not layout: err=%v", err)
	}
}
