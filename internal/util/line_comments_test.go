package util

import (
	"strings"
	"testing"
)

// Positive: whole-line and trailing comments are removed, the code before them is kept.
func TestStripHashCommentsRemovesComments(t *testing.T) {
	in := "# header\n      - uses: actions/checkout@v4 # pinned\n    # - uses: actions/checkout@v1\nrun: syft dir:dist -o spdx-json=x\t# trailing"
	want := "\n      - uses: actions/checkout@v4\n\nrun: syft dir:dist -o spdx-json=x"
	got, err := StripHashComments(in)
	if err != nil || got != want {
		t.Fatalf("StripHashComments = %q, %v; want %q", got, err, want)
	}
}

// Negative: a "#" inside a token is not a comment, and text without comments is unchanged.
func TestStripHashCommentsKeepsHashInsideTokens(t *testing.T) {
	for _, in := range []string{
		"uses: owner/repo@v1\nurl: https://example.com/page#section",
		"value: a#b",
		"",
	} {
		if got, err := StripHashComments(in); err != nil || got != in {
			t.Errorf("StripHashComments(%q) = %q, %v; want it unchanged", in, got, err)
		}
	}
}

// Boundary: exactly the line bound is accepted and one more line is refused.
func TestStripHashCommentsLineBound(t *testing.T) {
	atBound := strings.Repeat("a\n", maxCommentScanLines-1) + "a"
	if _, err := StripHashComments(atBound); err != nil {
		t.Fatalf("%d lines refused: %v", maxCommentScanLines, err)
	}
	if _, err := StripHashComments(atBound + "\na"); err == nil {
		t.Fatalf("%d lines accepted", maxCommentScanLines+1)
	}
}
