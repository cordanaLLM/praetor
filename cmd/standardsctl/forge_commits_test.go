package main

import (
	"strings"
	"testing"
)

// The HISS-14 commit gate is only real if the command exists and refuses bad input.
func TestForgeCheckCommits_Positive_EmptyRange(t *testing.T) {
	if err := dispatchCommand("forge", []string{"check-commits", "--repo=../..", "--base=HEAD", "--head=HEAD"}); err != nil {
		t.Fatalf("expected an empty commit range to pass, got %v", err)
	}
}

func TestForgeCheckCommits_Negative_MissingBaseAndHostileRevision(t *testing.T) {
	if err := dispatchCommand("forge", []string{"check-commits"}); err == nil {
		t.Fatal("expected an error when --base is missing")
	}

	err := dispatchCommand("forge", []string{"check-commits", "--repo=../..", "--base=HEAD;rm -rf /", "--head=HEAD"})
	if err == nil {
		t.Fatal("expected a shell metacharacter in a revision to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid git revision") {
		t.Fatalf("expected the revision validation error, got %v", err)
	}
}

func TestForgeCheckCommits_Boundary_UnknownRevision(t *testing.T) {
	err := dispatchCommand("forge", []string{
		"check-commits", "--repo=../..", "--base=0000000000000000000000000000000000000000", "--head=HEAD",
	})
	if err == nil {
		t.Fatal("expected an error for a revision that does not exist")
	}
}
