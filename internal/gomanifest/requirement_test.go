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

// Positive: a bare, a commented and a quoted requirement yield the module path and version
// modfile records, never a token with its quotes or its comment still attached.
func TestParseRequirement_Positive_BareCommentedAndQuoted(t *testing.T) {
	cases := map[string]string{
		"bare":            "example.com/a v1.0.0",
		"comment":         "example.com/a v1.0.0 // kept for the CLI",
		"glued comment":   "example.com/a v1.0.0//indirect",
		"quoted path":     `"example.com/a" v1.0.0`,
		"quoted both":     `"example.com/a" "v1.0.0"`,
		"tab separated":   "\"example.com/a\"\tv1.0.0",
		"escaped literal": `"example.com/\u0061" v1.0.0`,
	}
	for name, line := range cases {
		requirement, ok := ParseRequirement(line)
		if !ok || requirement != (Requirement{Path: "example.com/a", Version: "v1.0.0"}) {
			t.Errorf("%s: ParseRequirement(%q) = %+v, %v; want example.com/a v1.0.0", name, line, requirement, ok)
		}
	}
}

// Negative: modfile rejects a double-quoted token that does not unquote and a quote
// character inside an unquoted token, so neither becomes a requirement.
func TestParseRequirement_Negative_MalformedQuotes(t *testing.T) {
	for _, line := range []string{
		`"example.com/a v1.0.0`,
		`'example.com/a' v1.0.0`,
		"`example.com/a` v1.0.0",
		`example.com/"a" v1.0.0`,
		`example.com/a "v1.0.0`,
	} {
		if requirement, ok := ParseRequirement(line); ok {
			t.Errorf("ParseRequirement(%q) = %+v, want refusal", line, requirement)
		}
	}
}

// Boundary: a line missing its version, an empty quoted path or version, and an empty or
// comment-only line name no requirement.
func TestParseRequirement_Boundary_MissingTokens(t *testing.T) {
	for _, line := range []string{
		"example.com/a", `"example.com/a"`, `"" v1.0.0`, `example.com/a ""`,
		"example.com/a // v1.0.0", "", "// indirect",
	} {
		if requirement, ok := ParseRequirement(line); ok {
			t.Errorf("ParseRequirement(%q) = %+v, want refusal", line, requirement)
		}
	}
}

// ToolLine: a single-line directive and a block both identify each tool path (positive); a
// require line, a toolchain directive and a nil state identify none (negative); and a block
// that closes leaves later lines outside it (boundary).
func TestToolLineTransitions(t *testing.T) {
	if line, ok := ToolLine("tool example.com/cmd/x", nil); ok || line != "" {
		t.Fatal("nil state accepted")
	}
	cases := []struct {
		line, want    string
		tool, inBlock bool
	}{
		{"tool example.com/cmd/a", "example.com/cmd/a", true, false},
		{"toolchain go1.27.0", "toolchain go1.27.0", false, false},
		{"require example.com/a v1.0.0", "require example.com/a v1.0.0", false, false},
		{"tool (", "", false, true},
		{"\texample.com/cmd/b // the linter", "example.com/cmd/b // the linter", true, true},
		{")", "", false, false},
		{"example.com/cmd/c", "example.com/cmd/c", false, false},
	}
	inBlock := false
	for _, tc := range cases {
		line, tool := ToolLine(tc.line, &inBlock)
		if line != tc.want || tool != tc.tool || inBlock != tc.inBlock {
			t.Fatalf("%q: line=%q tool=%v block=%v", tc.line, line, tool, inBlock)
		}
	}
}
