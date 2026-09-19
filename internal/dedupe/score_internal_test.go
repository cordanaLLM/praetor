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

// =========================================================================
// A sprawl finding the verdict never mentions is a finding nobody acts on (praetor#295)
// =========================================================================

// TestCalculateScore_Negative_SingleSprawlItemFails is the regression: five points per
// sprawl item against an 80 threshold let four ad-hoc utility call sites score 80 and pass.
func TestCalculateScore_Negative_SingleSprawlItemFails(t *testing.T) {
	report := &DedupeReport{TotalFilesScanned: 4, SprawlItems: []SprawlItem{{File: "a.go", Line: 3}}}
	calculateScore(report)
	if report.CleanlinessScore != 95 {
		t.Errorf("one sprawl item deducts 5, got %.1f", report.CleanlinessScore)
	}
	if report.Passed {
		t.Error("a repository with an unresolved sprawl finding must not pass")
	}
}

// TestCalculateScore_Boundary_FourSprawlItemsLandOnTheThreshold pins the exact score that
// used to pass: four findings, 80.0, no duplicates.
func TestCalculateScore_Boundary_FourSprawlItemsLandOnTheThreshold(t *testing.T) {
	report := &DedupeReport{TotalFilesScanned: 9, SprawlItems: make([]SprawlItem, 4)}
	calculateScore(report)
	if report.CleanlinessScore != 80 {
		t.Errorf("four sprawl items deduct 20, got %.1f", report.CleanlinessScore)
	}
	if report.Passed {
		t.Error("four sprawl findings on the pass line must not pass")
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
