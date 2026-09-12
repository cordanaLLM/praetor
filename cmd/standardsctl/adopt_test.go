package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestReorderAdoptArgs_Positive(t *testing.T) {
	got := reorderAdoptArgs([]string{"repo-dir", "--profile", "framework", "--dry-run"})
	want := "--profile framework --dry-run repo-dir"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestReorderAdoptArgs_Negative_BoolFlagsNeverConsumeAPath(t *testing.T) {
	for _, flagName := range []string{"--dry-run", "-force", "--record-baseline", "--all-missing"} {
		got := reorderAdoptArgs([]string{flagName, "repo-dir"})
		if len(got) != 2 || got[0] != flagName || got[1] != "repo-dir" {
			t.Errorf("%s must not swallow the positional path, got %v", flagName, got)
		}
	}
}

func TestReorderAdoptArgs_Boundary(t *testing.T) {
	if got := reorderAdoptArgs(nil); len(got) != 0 {
		t.Fatalf("nil args must yield no args, got %v", got)
	}
	if got := reorderAdoptArgs([]string{"--profile=framework", "repo"}); strings.Join(got, " ") != "--profile=framework repo" {
		t.Fatalf("flag=value form must stay intact, got %v", got)
	}
	if got := reorderAdoptArgs([]string{"--profile"}); len(got) != 1 {
		t.Fatalf("a trailing value flag has no value to consume, got %v", got)
	}
	if got := reorderAdoptArgs([]string{"--profile", "--dry-run"}); len(got) != 2 {
		t.Fatalf("a flag following a value flag is not its value, got %v", got)
	}
}

func TestSplitFacets_3D(t *testing.T) {
	if got := splitFacets("a:b, c:d ,,"); len(got) != 2 || got[0] != "a:b" || got[1] != "c:d" {
		t.Fatalf("positive: got %v", got)
	}
	if got := splitFacets(""); len(got) != 0 {
		t.Fatalf("negative: empty input must yield no facets, got %v", got)
	}
	if got := splitFacets(" , "); len(got) != 0 {
		t.Fatalf("boundary: whitespace-only entries are dropped, got %v", got)
	}
}

func TestPrintBatchResult_ReportsErrorsAsFailure(t *testing.T) {
	ok := printBatchResult("r", false, &adopt.AdoptReport{Errors: []string{"boom"}}, nil)
	if ok {
		t.Fatal("a report with errors is a failed adoption")
	}
	if printBatchResult("r", false, nil, errors.New("hard failure")) {
		t.Fatal("an error is a failed adoption")
	}
	if !printBatchResult("r", true, &adopt.AdoptReport{Warnings: []string{"soft"}}, nil) {
		t.Fatal("warnings alone do not fail an adoption")
	}
}
