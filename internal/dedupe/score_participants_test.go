package dedupe

import "testing"

// TestScoreDistinctParticipants counts functions, not finding records.
func TestScoreDistinctParticipants(t *testing.T) {
	loc := func(line int) FileLocation { return FileLocation{Path: "a.go", Line: line} }
	report := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 10,
		Duplicates: []DuplicateGroup{{Locations: []FileLocation{loc(1), loc(2)}}, {Locations: []FileLocation{loc(1), loc(3), loc(4), loc(5)}}}}
	calculateScore(report)
	if report.CleanlinessScore != 50 || report.Passed || report.FindingFunctions != 5 {
		t.Fatalf("score=%v passed=%v functions=%d; want 50, false, 5", report.CleanlinessScore, report.Passed, report.FindingFunctions)
	}
}

// TestScoreSprawlSharesParticipant counts repeated findings in a single function once.
func TestScoreSprawlSharesParticipant(t *testing.T) {
	report := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 10,
		Duplicates:  []DuplicateGroup{{Locations: []FileLocation{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 10}}}},
		SprawlItems: []SprawlItem{{File: "a.go", Line: 3, funcLine: 1}, {File: "a.go", Line: 4, funcLine: 1}}}
	calculateScore(report)
	if report.CleanlinessScore != 80 || report.Passed {
		t.Fatalf("score=%v passed=%v; want 80 and false", report.CleanlinessScore, report.Passed)
	}
}

// TestScoreDuplicateGroupSize compares distinct participants in groups of different sizes.
func TestScoreDuplicateGroupSize(t *testing.T) {
	locations := []FileLocation{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 2}, {Path: "a.go", Line: 3}, {Path: "a.go", Line: 4}}
	small := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 10, Duplicates: []DuplicateGroup{{Locations: locations[:2]}}}
	large := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 10, Duplicates: []DuplicateGroup{{Locations: locations}}}
	calculateScore(small)
	calculateScore(large)
	if small.CleanlinessScore != 80 || large.CleanlinessScore != 60 || small.Passed || large.Passed {
		t.Fatalf("small=%v large=%v", small, large)
	}
}
