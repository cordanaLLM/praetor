package gomanifest

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: single-line and block replaces by a directory are local, with or without a comment.
func TestLocalReplaces_Positive_SingleBlockAndComment(t *testing.T) {
	manifest := "module m\n\nreplace example.com/a => ../..\n\nreplace (\n\texample.com/b => ./vendor/b // local copy\n\texample.com/c v1.0.0 => ../c\n)\n"
	got := LocalReplaces([]byte(manifest))
	want := map[string]bool{"example.com/a": true, "example.com/b": true, "example.com/c": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LocalReplaces = %v, want %v", got, want)
	}
}

// Negative: a replacement by a versioned module is not local, a require line is not a replace,
// and a manifest without replaces has none.
func TestLocalReplaces_Negative_VersionedAndRequire(t *testing.T) {
	manifest := "require example.com/a v1.0.0\nreplace example.com/b => example.com/fork v1.2.3\nreplace (\n\texample.com/c v1.0.0 => example.com/d v2.0.0 // pinned\n)\n"
	if got := LocalReplaces([]byte(manifest)); len(got) != 0 {
		t.Fatalf("LocalReplaces = %v, want none", got)
	}
}

// Boundary: an empty manifest, a leading byte-order mark, and a line past the line bound.
func TestLocalReplaces_Boundary_EmptyBOMAndLineBound(t *testing.T) {
	if got := LocalReplaces(nil); len(got) != 0 {
		t.Fatalf("empty manifest: %v", got)
	}
	if got := LocalReplaces([]byte("\xef\xbb\xbfreplace example.com/a => ../a\n")); !got["example.com/a"] {
		t.Fatalf("a replace on the first line behind a byte-order mark is read, got %v", got)
	}
	last := "replace example.com/last => ../last\n"
	if got := LocalReplaces([]byte(strings.Repeat("\n", maxManifestLines-1) + last)); !got["example.com/last"] {
		t.Fatalf("a replace on the last line inside the bound is read, got %v", got)
	}
	if got := LocalReplaces([]byte(strings.Repeat("\n", maxManifestLines) + last)); len(got) != 0 {
		t.Fatalf("a replace past the line bound is not read, got %v", got)
	}
}
