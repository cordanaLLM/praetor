package util

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// exactDigest is the SHA-256 of text's bytes as they are.
func exactDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Positive: an LF text keeps the digest of its bytes, and its CRLF checkout hashes alike with
// no strict reason, so a lock pinned from LF blobs verifies on a Windows checkout (#781).
func TestCheckoutTextDigestPositiveFoldsOneConsistentStyle(t *testing.T) {
	const lf = "id: framework\nname: Framework\n"
	digest, strict := CheckoutTextDigest([]byte(lf))
	if strict != "" || digest != exactDigest(lf) {
		t.Fatalf("LF text: digest=%s strict=%q, want the digest of its bytes", digest, strict)
	}
	crlf, strict := CheckoutTextDigest([]byte(strings.ReplaceAll(lf, "\n", "\r\n")))
	if strict != "" || crlf != digest {
		t.Fatalf("CRLF checkout: digest=%s strict=%q, want %s", crlf, strict, digest)
	}
}

// Negative: an edit still moves the digest, and a mixed or lone-carriage-return text hashes its
// exact bytes and says why, never folded into the LF digest.
func TestCheckoutTextDigestNegativeKeepsEditsAndMixedEndingsExact(t *testing.T) {
	original, _ := CheckoutTextDigest([]byte("max: 1\r\n"))
	if edited, _ := CheckoutTextDigest([]byte("max: 2\r\n")); edited == original {
		t.Fatal("an edited CRLF text must digest differently")
	}
	lf, _ := CheckoutTextDigest([]byte("a\nb\n"))
	for text, reason := range map[string]string{"a\r\nb\n": "mixed", "a\rb\n": "lone carriage return"} {
		digest, strict := CheckoutTextDigest([]byte(text))
		if digest != exactDigest(text) || digest == lf || !strings.Contains(strict, reason) {
			t.Errorf("%q: digest=%s strict=%q, want the exact bytes and a %q reason", text, digest, strict, reason)
		}
	}
}

// Boundary: an empty text and a text without a line ending hash their bytes with no reason.
func TestCheckoutTextDigestBoundaryEmptyAndUnterminated(t *testing.T) {
	for _, text := range []string{"", "x"} {
		if digest, strict := CheckoutTextDigest([]byte(text)); digest != exactDigest(text) || strict != "" {
			t.Errorf("%q: digest=%s strict=%q", text, digest, strict)
		}
	}
}

// Positive: a CRLF checkout equals its LF source, and the other way round.
func TestCheckoutTextEqualPositiveFoldsOneConsistentStyle(t *testing.T) {
	for _, pair := range [][2]string{{"a\r\nb\r\n", "a\nb\n"}, {"a\nb\n", "a\r\nb\r\n"}, {"a\nb\n", "a\nb\n"}} {
		if equal, strict := CheckoutTextEqual([]byte(pair[0]), []byte(pair[1])); !equal || strict != "" {
			t.Errorf("%q vs %q: equal=%v strict=%q", pair[0], pair[1], equal, strict)
		}
	}
}

// Negative: an edit differs in either style, and a mixed text is compared byte for byte, so it
// differs from its LF form and says why; the note names the exact comparison.
func TestCheckoutTextEqualNegativeEditsAndMixedEndings(t *testing.T) {
	if equal, strict := CheckoutTextEqual([]byte("a\r\nc\r\n"), []byte("a\nb\n")); equal || strict != "" {
		t.Fatalf("an edit: equal=%v strict=%q", equal, strict)
	}
	equal, strict := CheckoutTextEqual([]byte("a\r\nb\n"), []byte("a\nb\n"))
	if equal || !strings.Contains(strict, "mixed") {
		t.Fatalf("a mixed checkout: equal=%v strict=%q", equal, strict)
	}
	if note := ByteExactNote(strict); !strings.Contains(note, "compared byte for byte: ") || !strings.Contains(note, "mixed") {
		t.Fatalf("note = %q", note)
	}
}

// Boundary: identical mixed bytes are still equal (the exact comparison, not a refusal), and a
// folded comparison adds no note.
func TestCheckoutTextEqualBoundaryIdenticalMixedBytes(t *testing.T) {
	if equal, strict := CheckoutTextEqual([]byte("a\r\nb\n"), []byte("a\r\nb\n")); !equal || strict == "" {
		t.Fatalf("identical mixed bytes: equal=%v strict=%q", equal, strict)
	}
	if note := ByteExactNote(""); note != "" {
		t.Fatalf("note without a strict reason = %q", note)
	}
}
