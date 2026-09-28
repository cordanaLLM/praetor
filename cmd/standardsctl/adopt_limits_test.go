package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// largeHarnessRepo is a repository declaring its identity, with go.mod and enough files under
// src/ that its walk meets entries entries in all.
func largeHarnessRepo(t *testing.T, entries int) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "repository:\n  owner: acme\n  name: widget\n")
	writeFixtureFile(t, root, "go.mod", "module example.com/widget\n\ngo 1.27\n")
	for i := 0; i < entries-3; i++ {
		writeFixtureFile(t, root, "src/f"+strconv.Itoa(i), "")
	}
	return root
}

// TestPaperclipHarness_VerificationLimitFlags: `paperclip harness` takes the same
// --verification-max-* flags as adopt, so a large repository is read as far as adoption reads it
// (issue #535). Positive: a raised entry bound writes the harness. Negative: the default bound
// fails naming the flag that raises it, a value past the ceiling is refused naming its range, and
// a non-numeric value fails flag parsing. Boundary: exactly the default number of entries passes.
func TestPaperclipHarness_VerificationLimitFlags(t *testing.T) {
	defaults := adopt.DefaultVerificationLimits()
	large := largeHarnessRepo(t, defaults.MaxEntries+1)
	err := runPaperclipHarness(t.Context(), []string{"--path", large})
	if err == nil || !strings.Contains(err.Error(), "raise max_entries with --"+adopt.VerificationEntriesFlag) {
		t.Fatalf("default bound on a large repository = %v; want it to name --%s", err, adopt.VerificationEntriesFlag)
	}
	over := fmt.Sprintf("--%s=%d", adopt.VerificationEntriesFlag, adopt.VerificationEntriesCeiling+1)
	if err := runPaperclipHarness(t.Context(), []string{"--path", large, over}); err == nil || !strings.Contains(err.Error(), "must be 1..200000") {
		t.Fatalf("over-ceiling flag = %v; want a refusal naming the range", err)
	}
	if err := runPaperclipHarness(t.Context(), []string{"--path", large, "--verification-max-files=many"}); err == nil || !strings.Contains(err.Error(), adopt.VerificationFilesFlag) {
		t.Fatalf("a non-numeric bound must fail flag parsing: %v", err)
	}
	raised := fmt.Sprintf("--%s=%d", adopt.VerificationEntriesFlag, 2*defaults.MaxEntries)
	if err := runPaperclipHarness(t.Context(), []string{"--path", large, raised}); err != nil {
		t.Fatalf("raised bound refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(large, ".paperclip", "harness.json")); err != nil {
		t.Fatalf("raised bound wrote no harness: %v", err)
	}
	if err := runPaperclipHarness(t.Context(), []string{"--path", largeHarnessRepo(t, defaults.MaxEntries)}); err != nil {
		t.Fatalf("exactly %d entries refused: %v", defaults.MaxEntries, err)
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
