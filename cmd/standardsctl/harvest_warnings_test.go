package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func bundleWarnings(t *testing.T, rep *harvester.WorkstationBundleReport) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		printBundleWarnings(rep)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Positive: a bundle holding redacted configuration reports how many credential values
// were replaced beside the credential-bearing categories (BUG-608).
func TestPrintBundleWarningsReportsRedactions(t *testing.T) {
	out := bundleWarnings(t, &harvester.WorkstationBundleReport{
		Categories: map[string]int{"claude-desktop-config": 1}, RedactedValues: 3,
	})
	if !strings.Contains(out, "claude-desktop-config (1)") || !strings.Contains(out, "Redacted 3 credential values") {
		t.Fatalf("redaction warning missing:\n%s", out)
	}
}

// Negative: a bundle without credential-bearing categories prints no redaction warning.
func TestPrintBundleWarningsQuietWithoutSensitiveCategories(t *testing.T) {
	out := bundleWarnings(t, &harvester.WorkstationBundleReport{Categories: map[string]int{"skill": 4}})
	if strings.Contains(out, "[WARNING]") {
		t.Fatalf("unexpected warning:\n%s", out)
	}
}

// Boundary: zero redactions in a sensitive bundle is reported as zero, not hidden, so the
// operator sees that nothing was removed from files the bundle still carries.
func TestPrintBundleWarningsReportsZeroRedactions(t *testing.T) {
	out := bundleWarnings(t, &harvester.WorkstationBundleReport{Categories: map[string]int{"cli-history": 1}})
	if !strings.Contains(out, "Redacted 0 credential values") {
		t.Fatalf("zero redactions hidden:\n%s", out)
	}
}
