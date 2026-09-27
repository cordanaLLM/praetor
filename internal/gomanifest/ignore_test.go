package gomanifest

import (
	"path/filepath"
	"testing"
)

// Positive: single-line and block ignore directives are identified, their delimiters
// consumed, and their paths read with comments dropped and quotes removed.
func TestIgnoreLine_Positive_SingleAndBlock(t *testing.T) {
	cases := []struct {
		line, want, path string
		ignore, inBlock  bool
	}{
		{"ignore ./node_modules", "./node_modules", "./node_modules", true, false},
		{"ignore (", "", "", false, true},
		{"\tstatic // generated site", "static // generated site", "static", true, true},
		{"\t\"content/html\"", "\"content/html\"", "content/html", true, true},
		{")", "", "", false, false},
		{"require example.com/a v1.0.0", "require example.com/a v1.0.0", "", false, false},
	}
	inBlock := false
	for _, tc := range cases {
		line, ignore := IgnoreLine(tc.line, &inBlock)
		if line != tc.want || ignore != tc.ignore || inBlock != tc.inBlock {
			t.Fatalf("%q: line=%q ignore=%v block=%v", tc.line, line, ignore, inBlock)
		}
		if !ignore {
			continue
		}
		if path, ok := ParseIgnore(line); !ok || path != tc.path {
			t.Fatalf("ParseIgnore(%q) = %q, %v; want %q", line, path, ok, tc.path)
		}
	}
}

// Negative: a nil state identifies nothing, and an ignore line the go command refuses
// (no argument, two arguments, a malformed or empty quoted path) yields no path.
func TestIgnoreLine_Negative_NilStateAndMalformedPaths(t *testing.T) {
	if line, ok := IgnoreLine("ignore ./x", nil); ok || line != "" {
		t.Fatalf("nil state accepted: %q", line)
	}
	for _, line := range []string{"", "// only a comment", "a b", "\"unterminated", "pa\"th", "\"\""} {
		if path, ok := ParseIgnore(line); ok {
			t.Errorf("ParseIgnore(%q) = %q, want refused", line, path)
		}
	}
}

// Boundary: a keyword glued to its argument is no directive, and a blank or comment line
// inside a block is consumed without leaving it.
func TestIgnoreLine_Boundary_GluedKeywordAndBlockComments(t *testing.T) {
	inBlock := false
	if _, ok := IgnoreLine("ignored ./x", &inBlock); ok {
		t.Fatal("a glued keyword was read as an ignore directive")
	}
	IgnoreLine("ignore (", &inBlock)
	for _, line := range []string{"", "// kept out of ./...", "   "} {
		if _, ok := IgnoreLine(line, &inBlock); ok || !inBlock {
			t.Fatalf("%q: ok=%v block=%v", line, ok, inBlock)
		}
	}
}

// Positive: a rooted path ignores that directory below the root and its subtree; any other
// path ignores the directory at any depth, as cmd/go/internal/search.IgnorePatterns does.
func TestIgnoreSet_Positive_RootedAndAnywhere(t *testing.T) {
	set := NewIgnoreSet([]string{"./third_party/js", "static", "content/html"})
	for _, dir := range []string{
		"third_party/js", "third_party/js/vendor", "static", "web/static", "web/static/css",
		"content/html", "site/content/html/pages", filepath.Join("web", "static"),
	} {
		if !set.Ignores(dir) {
			t.Errorf("Ignores(%q) = false, want true", dir)
		}
	}
}

// Negative: a rooted path ignores nothing deeper than the root, and a pattern matches whole
// path elements only.
func TestIgnoreSet_Negative_ElementBoundaries(t *testing.T) {
	set := NewIgnoreSet([]string{"./third_party/js", "static", "content/html"})
	for _, dir := range []string{
		"web/third_party/js", "third_party", "third_party/jsx", "statics", "web/staticfiles",
		"content", "content/htmlx", "html",
	} {
		if set.Ignores(dir) {
			t.Errorf("Ignores(%q) = true, want false", dir)
		}
	}
}

// Boundary: the module root is never ignored, and the zero set ignores nothing.
func TestIgnoreSet_Boundary_RootAndZeroValue(t *testing.T) {
	set := NewIgnoreSet([]string{"./", "static"})
	for _, dir := range []string{"", "."} {
		if set.Ignores(dir) {
			t.Errorf("Ignores(%q) ignored the module root", dir)
		}
	}
	if !set.Ignores("anything") {
		t.Error(`"./" did not ignore every directory below the root, as the go command does`)
	}
	if (IgnoreSet{}).Ignores("static") {
		t.Error("the zero set ignored a directory")
	}
}
