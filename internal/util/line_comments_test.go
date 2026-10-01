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

// Positive: StripComments cuts line and block comments, keeps a block comment that spans
// lines open across the call, and reads a block comment as one blank.
func TestStripCommentsCutsComments(t *testing.T) {
	slash := CommentSyntax{Line: []string{"//"}, Block: [][2]string{{"/*", "*/"}}}
	markup := CommentSyntax{Block: [][2]string{{"<!--", "-->"}}}
	batch := CommentSyntax{Line: []string{"REM", "::"}}
	cases := []struct {
		line   string
		syntax CommentSyntax
		open   string
		want   string
		still  string
	}{
		{"run(a) /* old */", slash, "", "run(a)", ""},
		{"run(a/*x*/b) // note", slash, "", "run(a b)", ""},
		{"run() /* opens", slash, "", "run()", "*/"},
		{"still a comment", slash, "*/", "", "*/"},
		{"ends */ run()", slash, "*/", "  run()", ""},
		{"<a/> <!-- note --> <b/>", markup, "", "<a/>   <b/>", ""},
		{"REM set path", batch, "", "", ""},
		{"set x=1 :: note", batch, "", "set x=1", ""},
		{`msg: "a # b" # note`, CommentSyntax{Line: []string{"#"}}, "", `msg: "a # b"`, ""},
		{`print('// x') // note`, slash, "", `print('// x')`, ""},
	}
	for _, tc := range cases {
		got, still := StripComments(tc.line, tc.syntax, tc.open)
		if got != tc.want || still != tc.still {
			t.Errorf("StripComments(%q, %q) = %q, %q; want %q, %q", tc.line, tc.open, got, still, tc.want, tc.still)
		}
	}
}

// Negative: a marker inside a word or a quoted string, a word that only starts like a word
// marker, and a quote that nothing closes or that follows a letter leave the line unchanged.
func TestStripCommentsKeepsCode(t *testing.T) {
	hash := CommentSyntax{Line: []string{"#"}}
	cases := []struct {
		line   string
		syntax CommentSyntax
	}{
		{`msg: "a # b"`, hash},
		{`echo 'x # y'`, hash},
		{"REMOVE x", CommentSyntax{Line: []string{"REM"}}},
		{"glob: a/b*", CommentSyntax{Block: [][2]string{{"/*", "*/"}}}},
		{"no comment", CommentSyntax{}},
	}
	for _, tc := range cases {
		if got, still := StripComments(tc.line, tc.syntax, ""); got != tc.line || still != "" {
			t.Errorf("StripComments(%q) = %q, %q; want it unchanged", tc.line, got, still)
		}
	}
	// An apostrophe quotes nothing, so the comment after it is still cut.
	if got, _ := StripComments("x: don't # note", hash, ""); got != "x: don't" {
		t.Errorf("apostrophe opened a quote: %q", got)
	}
	if got, _ := StripComments(`x: "open # note`, hash, ""); got != `x: "open` {
		t.Errorf("unclosed quote hid a comment: %q", got)
	}
}

// Boundary: an empty line, an empty block opener, a closer as the last bytes of the line, a
// word marker as the whole line, and an open block on an empty line.
func TestStripCommentsBoundary(t *testing.T) {
	slash := CommentSyntax{Line: []string{"//"}, Block: [][2]string{{"/*", "*/"}}}
	if got, still := StripComments("", slash, ""); got != "" || still != "" {
		t.Errorf("empty line = %q, %q", got, still)
	}
	if got, still := StripComments("", slash, "*/"); got != "" || still != "*/" {
		t.Errorf("empty line inside a block = %q, %q", got, still)
	}
	if got, _ := StripComments("a /* b */", slash, ""); got != "a" {
		t.Errorf("closer at the end = %q", got)
	}
	if got, _ := StripComments("a b", CommentSyntax{Block: [][2]string{{"", "x"}}}, ""); got != "a b" {
		t.Errorf("empty opener = %q", got)
	}
	if got, _ := StripComments("REM", CommentSyntax{Line: []string{"REM"}}, ""); got != "" {
		t.Errorf("bare word marker = %q", got)
	}
	if got, err := StripHashComments(`run: echo "# kept" # cut`); err != nil || got != `run: echo "# kept"` {
		t.Errorf("StripHashComments quoted hash = %q, %v", got, err)
	}
}
