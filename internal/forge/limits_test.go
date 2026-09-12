package forge

import (
	"strings"
	"testing"
)

func TestNewForge_Boundary_EmptyAndMixedCaseProvider(t *testing.T) {
	if _, err := NewForge("", "token", ""); err == nil {
		t.Fatal("expected an error for an empty provider")
	}
	// Provider identifiers are matched exactly, so a mixed-case spelling cannot silently
	// select a different driver than the operator intended.
	if _, err := NewForge("GitHub", "token", ""); err == nil {
		t.Fatal("expected an error for a mixed-case provider identifier")
	}
	if _, err := NewForge("github", "", ""); err != nil {
		t.Fatalf("constructing a driver must not require a token: %v", err)
	}
}

func TestIssueDependencyParsing_Boundary_ScalarLimits(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < MaxDependenciesLimit+50; i++ {
		sb.WriteString("Depends-On: #1\n")
	}
	refs := ParseIssueDependencies(sb.String())
	if len(refs) != MaxDependenciesLimit {
		t.Fatalf("expected the parse to stop at %d refs, got %d", MaxDependenciesLimit, len(refs))
	}

	// Lines beyond the scalar line bound are never scanned.
	var padded strings.Builder
	for i := 0; i < MaxLinesLimit+5; i++ {
		padded.WriteString("filler\n")
	}
	padded.WriteString("Depends-On: #42\n")
	if got := ParseIssueDependencies(padded.String()); len(got) != 0 {
		t.Fatalf("expected no refs beyond the line bound, got %d", len(got))
	}

	// An issue number that overflows int is skipped rather than misparsed.
	if got := ParseIssueDependencies("Depends-On: #99999999999999999999999999\n"); len(got) != 0 {
		t.Fatalf("expected an unparsable issue number to be skipped, got %+v", got)
	}

	if got := ParseIssueDependencies("   \n\t\n"); len(got) != 0 {
		t.Fatalf("expected no refs for whitespace-only input, got %d", len(got))
	}
}
