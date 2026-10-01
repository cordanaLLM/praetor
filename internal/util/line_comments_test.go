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

// Positive: StripLineComment cuts at a whole-line or trailing comment of any marker.
func TestStripLineCommentMarkers(t *testing.T) {
	cases := []struct{ line, marker, want string }{
		{"run() // old note", "//", "run()"},
		{"// whole line", "//", ""},
		{"  select 1 -- note", "--", "  select 1"},
		{"key = 1\t; note", ";", "key = 1"},
		{"port: 8080 # note", "#", "port: 8080"},
	}
	for _, tc := range cases {
		if got := StripLineComment(tc.line, tc.marker); got != tc.want {
			t.Errorf("StripLineComment(%q, %q) = %q; want %q", tc.line, tc.marker, got, tc.want)
		}
	}
}

// Negative: a marker inside a token is kept, and an empty marker or a line without the
// marker is returned unchanged.
func TestStripLineCommentKeepsMarkerInsideTokens(t *testing.T) {
	cases := []struct{ line, marker string }{
		{`get("https://example.invalid/x")`, "//"},
		{"a--b", "--"},
		{"color: a#b", "#"},
		{"no comment here", "//"},
		{"run() // note", ""},
	}
	for _, tc := range cases {
		if got := StripLineComment(tc.line, tc.marker); got != tc.line {
			t.Errorf("StripLineComment(%q, %q) = %q; want it unchanged", tc.line, tc.marker, got)
		}
	}
}

// Boundary: an empty line, a line that is only the marker, a marker in token position before
// a real comment, and a marker as the last bytes of the line.
func TestStripLineCommentBoundary(t *testing.T) {
	cases := []struct{ line, marker, want string }{
		{"", "//", ""},
		{"//", "//", ""},
		{"http://a // b", "//", "http://a"},
		{"x //", "//", "x"},
		{"x/", "//", "x/"},
	}
	for _, tc := range cases {
		if got := StripLineComment(tc.line, tc.marker); got != tc.want {
			t.Errorf("StripLineComment(%q, %q) = %q; want %q", tc.line, tc.marker, got, tc.want)
		}
	}
}
