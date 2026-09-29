package cavemansource

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	declaredDigest  = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	extractedDigest = "sha256:" + "2222222222222222222222222222222222222222222222222222222222222222"
)

// TestVerifyDeclaredResult_Positive_MatchReturnsResult: a declaration equal to the extraction
// passes and hands the extraction back.
func TestVerifyDeclaredResult_Positive_MatchReturnsResult(t *testing.T) {
	result := Result{Applicable: 3, NotApplicable: 1, SHA256: declaredDigest}
	got, err := verifyDeclaredResult(result, &config.RegisterSources{Expected: 3, NotApplicable: 1, SHA256: declaredDigest})
	if err != nil || got.SHA256 != declaredDigest || got.Applicable != 3 {
		t.Fatalf("matching declaration rejected: %+v, %v", got, err)
	}
}

// TestVerifyDeclaredResult_Negative_ReportsEveryExtractedValue: a declaration off in all three
// fields fails with one error naming each extracted value, so one run of the configured-sources
// check yields expected, not_applicable and sha256 together (#502 U9 remedy).
func TestVerifyDeclaredResult_Negative_ReportsEveryExtractedValue(t *testing.T) {
	result := Result{Applicable: 5, NotApplicable: 2, SHA256: extractedDigest}
	_, err := verifyDeclaredResult(result, &config.RegisterSources{Expected: 4, NotApplicable: 1, SHA256: declaredDigest})
	if err == nil {
		t.Fatal("drifted declaration accepted")
	}
	for _, want := range []string{
		"register.sources expected 4 applicable values, extracted 5",
		"expected 1 not-applicable values, extracted 2",
		"sha256 mismatch: declared " + declaredDigest + ", actual " + extractedDigest,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mismatch error lacks %q: %v", want, err)
		}
	}
}

// TestVerifyDeclaredResult_Boundary_OnlyTheDifferingField: an edited value that keeps the
// counts changes only the digest, and the error names only that field.
func TestVerifyDeclaredResult_Boundary_OnlyTheDifferingField(t *testing.T) {
	result := Result{Applicable: 3, NotApplicable: 0, SHA256: extractedDigest}
	_, err := verifyDeclaredResult(result, &config.RegisterSources{Expected: 3, SHA256: declaredDigest})
	want := "register.sources sha256 mismatch: declared " + declaredDigest + ", actual " + extractedDigest
	if err == nil || err.Error() != want {
		t.Fatalf("digest-only drift = %v, want %q", err, want)
	}
}
