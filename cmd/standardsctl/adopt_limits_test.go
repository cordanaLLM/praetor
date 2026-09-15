package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestVerificationLimitsFromFlags_Positive(t *testing.T) {
	defaults := adopt.DefaultVerificationLimits()
	raised := verificationLimitsFromFlags(defaults.MaxEntries*2, 0, 0)
	if raised == nil || raised.MaxEntries != defaults.MaxEntries*2 {
		t.Fatalf("a raised entry bound must be applied: %+v", raised)
	}
	if raised.MaxFiles != defaults.MaxFiles || raised.MaxDepth != defaults.MaxDepth || raised.MaxTotalBytes != defaults.MaxTotalBytes {
		t.Fatalf("one raised bound keeps the other defaults: %+v", raised)
	}
	if _, err := adopt.NormalizeVerificationLimits(raised); err != nil {
		t.Fatalf("a doubled default is admitted by adoption: %v", err)
	}
}

func TestVerificationLimitsFromFlags_Negative(t *testing.T) {
	if got := verificationLimitsFromFlags(0, 0, 0); got != nil {
		t.Fatalf("no raised bound must keep adoption defaults, got %+v", got)
	}
	if _, err := adopt.NormalizeVerificationLimits(verificationLimitsFromFlags(-1, 0, 0)); err == nil {
		t.Fatal("a negative bound must be rejected by adoption, not silently defaulted")
	}
	err := runAdopt([]string{"--verification-max-entries=many", "--dry-run", "--path", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "verification-max-entries") {
		t.Fatalf("a non-numeric bound must fail flag parsing: %v", err)
	}
}

func TestVerificationLimitsFromFlags_Boundary(t *testing.T) {
	if _, err := adopt.NormalizeVerificationLimits(verificationLimitsFromFlags(adopt.VerificationEntriesCeiling, 0, 0)); err != nil {
		t.Fatalf("the ceiling itself is admitted: %v", err)
	}
	if _, err := adopt.NormalizeVerificationLimits(verificationLimitsFromFlags(adopt.VerificationEntriesCeiling+1, 0, 0)); err == nil {
		t.Fatal("one past the ceiling must be rejected")
	}
	if _, err := adopt.NormalizeVerificationLimits(verificationLimitsFromFlags(0, 0, adopt.VerificationDepthCeiling)); err != nil {
		t.Fatalf("the depth ceiling itself is admitted: %v", err)
	}
}
