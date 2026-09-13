package hiss

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestScanCoverageValidateAcceptsConsistentAndUnknownScope(t *testing.T) {
	for _, coverage := range []*ScanCoverage{
		nil,
		{},
		{FilesRead: 3, UnscannedFiles: 4, UnscannedByExtension: map[string]int{".cs": 2, "": 1}, UnlistedUnscannedFiles: 1},
		{FilesRead: math.MaxInt, UnscannedFiles: math.MaxInt, UnscannedByExtension: map[string]int{".cs": math.MaxInt - 1}, UnlistedUnscannedFiles: 1},
	} {
		if err := coverage.Validate(); err != nil {
			t.Fatalf("consistent or unknown coverage rejected: %+v: %v", coverage, err)
		}
	}
}

func TestScanCoverageValidateRejectsCounterContradictions(t *testing.T) {
	for _, test := range []struct {
		name     string
		coverage ScanCoverage
	}{
		{"negative reads", ScanCoverage{FilesRead: -1}},
		{"negative unscanned", ScanCoverage{UnscannedFiles: -1}},
		{"negative unlisted", ScanCoverage{UnlistedUnscannedFiles: -1}},
		{"negative extension", ScanCoverage{UnscannedByExtension: map[string]int{".cs": -1}}},
		{"unlisted exceeds total", ScanCoverage{UnscannedFiles: 1, UnlistedUnscannedFiles: 2}},
		{"missing extension counts", ScanCoverage{UnscannedFiles: 1}},
		{"extension exceeds total", ScanCoverage{UnscannedFiles: 1, UnscannedByExtension: map[string]int{".cs": 2}}},
		{"extension below total", ScanCoverage{UnscannedFiles: 2, UnscannedByExtension: map[string]int{".cs": 1}}},
		{"sum overflow", ScanCoverage{UnscannedFiles: 0, UnscannedByExtension: map[string]int{".cs": math.MaxInt, ".md": math.MaxInt, "": 2}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.coverage.Validate(); err == nil {
				t.Fatalf("inconsistent coverage accepted: %+v", test.coverage)
			}
		})
	}
}

func TestScanCoverageValidateExtensionBounds(t *testing.T) {
	extensions := make(map[string]int)
	for i := 0; i < 32; i++ {
		extensions[fmt.Sprintf(".e%02d", i)] = 1
	}
	coverage := &ScanCoverage{UnscannedFiles: 32, UnscannedByExtension: extensions}
	if err := coverage.Validate(); err != nil {
		t.Fatalf("exact extension entry bound rejected: %v", err)
	}
	extensions[".overflow"] = 1
	coverage.UnscannedFiles++
	if err := coverage.Validate(); err == nil {
		t.Fatal("extension map above its bound accepted")
	}
	coverage = &ScanCoverage{UnscannedFiles: 1, UnscannedByExtension: map[string]int{"." + strings.Repeat("x", 31): 1}}
	if err := coverage.Validate(); err != nil {
		t.Fatalf("exact extension byte bound rejected: %v", err)
	}
	coverage.UnscannedByExtension = map[string]int{"." + strings.Repeat("x", 32): 1}
	if err := coverage.Validate(); err == nil {
		t.Fatal("extension above its byte bound accepted")
	}
}

func TestHistoricalTruncatedScanCoverageIsPartialAndUnknown(t *testing.T) {
	report := &ScanReport{Truncated: true}
	evidence := report.CoverageEvidence()
	if !strings.Contains(evidence, "unknown") || !strings.Contains(evidence, "truncated") || !strings.Contains(evidence, "partial") {
		t.Fatalf("historical truncation must retain unknown partial scope: %s", evidence)
	}
}
