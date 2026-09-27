package gomanifest

import "testing"

func TestRequirementLineTransitions(t *testing.T) {
	if line, ok := RequirementLine("require example.com/a v1.0.0", nil); ok || line != "" {
		t.Fatal("nil state accepted")
	}
	cases := []struct {
		line, want          string
		dependency, inBlock bool
	}{
		{"module example.com/app", "module example.com/app", false, false},
		{" require example.com/a v1.0.0 ", "example.com/a v1.0.0", true, false},
		{"require (", "", false, true},
		{"// comment", "", false, true},
		{"\texample.com/b v2.0.0 // indirect", "example.com/b v2.0.0 // indirect", true, true},
		{")", "", false, false},
		{"", "", false, false},
	}
	inBlock := false
	for _, tc := range cases {
		line, dependency := RequirementLine(tc.line, &inBlock)
		if line != tc.want || dependency != tc.dependency || inBlock != tc.inBlock {
			t.Fatalf("%q: line=%q dependency=%v block=%v", tc.line, line, dependency, inBlock)
		}
	}
}

// Positive: the marker as the go command writes it, without the space, and with the
// "indirect;" continuation modfile accepts.
func TestIsIndirect_Positive_MarkerForms(t *testing.T) {
	for _, line := range []string{
		"example.com/a v1.0.0 // indirect",
		"example.com/a v1.0.0 //indirect",
		"example.com/a v1.0.0 //   indirect  ",
		"example.com/a v1.0.0 // indirect; pulled in by example.com/b",
	} {
		if !IsIndirect(line) {
			t.Errorf("IsIndirect(%q) = false, want true", line)
		}
	}
}

// Negative: a comment that merely contains the word, or starts with a longer word, does
// not mark the requirement indirect, and neither does a line without a comment.
func TestIsIndirect_Negative_WordInsideOtherComments(t *testing.T) {
	for _, line := range []string{
		"example.com/a v1.0.0",
		"example.com/a v1.0.0 // indirectly needed by the build",
		"example.com/a v1.0.0 // see indirect",
		"example.com/a v1.0.0 // indirect for now",
	} {
		if IsIndirect(line) {
			t.Errorf("IsIndirect(%q) = true, want false", line)
		}
	}
}

// Boundary: an empty comment and an empty line carry no marker.
func TestIsIndirect_Boundary_EmptyCommentAndLine(t *testing.T) {
	for _, line := range []string{"example.com/a v1.0.0 //", "", "//"} {
		if IsIndirect(line) {
			t.Errorf("IsIndirect(%q) = true, want false", line)
		}
	}
}
