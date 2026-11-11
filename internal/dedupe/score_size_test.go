package dedupe

import "testing"

// TestScoreScalesWithRepositorySize compares the same finding in different scan sizes.
func TestScoreScalesWithRepositorySize(t *testing.T) {
	small := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 10, Duplicates: make([]DuplicateGroup, 1)}
	large := &DedupeReport{TotalFilesScanned: 1, TotalFuncsScanned: 100, Duplicates: make([]DuplicateGroup, 1)}
	calculateScore(small)
	calculateScore(large)
	if large.CleanlinessScore <= small.CleanlinessScore {
		t.Fatalf("large %.1f must exceed small %.1f", large.CleanlinessScore, small.CleanlinessScore)
	}
}
