package main

import (
	"testing"
)

// printCategories captures what printBundleCategories writes for one tally.
func printCategories(t *testing.T, tally map[string]int) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		printBundleCategories(tally)
		return nil
	})
	if err != nil {
		t.Fatalf("print categories: %v", err)
	}
	return out
}

// TestPrintBundleCategories_Positive_SortedAndStableAcrossRuns repeats the print because a
// map-ordered listing agrees with itself often enough to pass a single run.
func TestPrintBundleCategories_Positive_SortedAndStableAcrossRuns(t *testing.T) {
	tally := map[string]int{"vault": 3, "agents": 1, "dotfiles": 7, "cli-history": 2, "manifests": 5}
	want := "Categories:\n  - agents: 1 files\n  - cli-history: 2 files\n  - dotfiles: 7 files\n" +
		"  - manifests: 5 files\n  - vault: 3 files\n"
	for i := 0; i < 32; i++ {
		if got := printCategories(t, tally); got != want {
			t.Fatalf("run %d printed\n%s\nwant\n%s", i, got, want)
		}
	}
}

// TestPrintBundleCategories_Negative_EmptyTallyPrintsHeaderOnly: nothing bundled lists no
// category, and a nil tally is the same as an empty one.
func TestPrintBundleCategories_Negative_EmptyTallyPrintsHeaderOnly(t *testing.T) {
	for _, tally := range []map[string]int{nil, {}} {
		if got := printCategories(t, tally); got != "Categories:\n" {
			t.Fatalf("empty tally printed %q", got)
		}
	}
}

// TestPrintBundleCategories_Boundary_SingleCategory prints exactly one line under the header.
func TestPrintBundleCategories_Boundary_SingleCategory(t *testing.T) {
	if got := printCategories(t, map[string]int{"only": 0}); got != "Categories:\n  - only: 0 files\n" {
		t.Fatalf("single category printed %q", got)
	}
}
