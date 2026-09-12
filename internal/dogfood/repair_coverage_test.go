package dogfood

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

func TestRepairRejectsInconsistentRetainedPublicCoverage(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*PublicRepositoryResult)
	}{
		{"negative original coverage", func(item *PublicRepositoryResult) {
			item.OriginalScan.Coverage = &hiss.ScanCoverage{FilesRead: -1}
		}},
		{"contradictory verified coverage", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.Scan.Coverage = &hiss.ScanCoverage{
				UnscannedFiles: 2, UnscannedByExtension: map[string]int{".cs": 1},
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRepairPolicyFixture(t, report)
			test.mutate(&changed.Cases[0].Public.Results[0])
			if _, err := loadRepairPolicyFixture(t, changed); err == nil || !strings.Contains(err.Error(), "coverage") {
				t.Fatalf("inconsistent retained coverage was not rejected: %v", err)
			}
		})
	}
}

func TestRepairRetainedCoverageAllowsMeasuredAndHistoricalUnknown(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	if _, err := loadRepairPolicyFixture(t, report); err != nil {
		t.Fatalf("actual measured coverage rejected: %v", err)
	}
	item := &report.Cases[0].Public.Results[0]
	item.OriginalScan.Coverage = nil
	for i := range item.Attempts {
		item.Attempts[i].Verification.Scan.Coverage = nil
	}
	loaded, err := loadRepairPolicyFixture(t, report)
	if err != nil {
		t.Fatalf("historical unknown coverage rejected: %v", err)
	}
	retained := loaded.Cases[0].Public.Results[0]
	if retained.OriginalScan.Coverage != nil || retained.Attempts[0].Verification.Scan.Coverage != nil {
		t.Fatal("unknown historical coverage was replaced with measured scope")
	}
}
