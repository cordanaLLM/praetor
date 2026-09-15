package dedupe

import "testing"

// =========================================================================
// A score over an empty set is not a result (praetor#90)
// =========================================================================

// TestCalculateScore_Negative_EmptyScanIsNotClean is the regression.
//
// Measured on a repository with 510 TypeScript and Svelte files: zero scanned, "Cleanliness Score:
// 100.0%", "Passed: true". The detector reads Go sources only, so on any non-Go repository it
// certified a tree it never opened.
func TestCalculateScore_Negative_EmptyScanIsNotClean(t *testing.T) {
	report := &DedupeReport{Duplicates: nil, SprawlItems: nil, TotalFilesScanned: 0}
	calculateScore(report)
	if report.Applicable {
		t.Error("a scan that read no files must not report itself applicable")
	}
	if report.Passed {
		t.Error("an unexamined repository must not pass")
	}
	if report.CleanlinessScore != 0 {
		t.Errorf("a score over an empty set is not a result, got %.1f", report.CleanlinessScore)
	}
}

// TestCalculateScore_Positive_ScannedCleanRepositoryStillPasses confirms the guard did not turn the
// ordinary case into a failure.
func TestCalculateScore_Positive_ScannedCleanRepositoryStillPasses(t *testing.T) {
	report := &DedupeReport{TotalFilesScanned: 12, TotalFuncsScanned: 40}
	calculateScore(report)
	if !report.Applicable {
		t.Error("a repository with Go sources is applicable")
	}
	if !report.Passed || report.CleanlinessScore != 100 {
		t.Errorf("a clean scanned repository must still pass at 100, got %.1f passed=%v",
			report.CleanlinessScore, report.Passed)
	}
}

// TestCalculateScore_Boundary_DuplicatesStillDeduct checks that applicability did not displace the
// actual detection: a scanned repository with clones must still fail.
func TestCalculateScore_Boundary_DuplicatesStillDeduct(t *testing.T) {
	report := &DedupeReport{
		TotalFilesScanned: 3,
		Duplicates:        []DuplicateGroup{{Hash: "a", LOC: 20}, {Hash: "b", LOC: 30}},
	}
	calculateScore(report)
	if !report.Applicable {
		t.Error("a scanned repository is applicable even when it fails")
	}
	if report.Passed {
		t.Error("duplicates must still fail a scanned repository")
	}
	if report.CleanlinessScore != 70 {
		t.Errorf("two duplicate groups deduct 30, got %.1f", report.CleanlinessScore)
	}
}
