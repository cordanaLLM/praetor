package gomanifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeModule(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+module+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: single-line and block replaces by a directory of the checkout whose go.mod names the
// replaced module are local, with or without a comment.
func TestLocalReplaces_Positive_SingleBlockAndComment(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "tools", "x")
	writeModule(t, root, "example.com/a")
	writeModule(t, filepath.Join(root, "vendor", "b"), "example.com/b")
	writeModule(t, filepath.Join(root, "c"), "example.com/c")
	manifest := "module m\n\nreplace example.com/a => ../..\n\nreplace (\n\texample.com/b => ../../vendor/b // local copy\n\texample.com/c v1.0.0 => ../../c\n)\n"
	got := LocalReplaces([]byte(manifest), nested, root)
	want := map[string]bool{"example.com/a": true, "example.com/b": true, "example.com/c": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LocalReplaces = %v, want %v", got, want)
	}
}

// Negative: a replacement by a versioned module, a directory that names another module, has no
// go.mod, or leaves the checkout (relative, absolute, or through a symlink) is not local.
func TestLocalReplaces_Negative_NotOwnModule(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "checkout")
	writeModule(t, filepath.Join(root, "third_party", "fork"), "example.com/fork")
	writeModule(t, filepath.Join(root, "empty"), "")
	writeModule(t, filepath.Join(parent, "sibling"), "example.com/sibling")
	if err := os.Symlink(filepath.Join(parent, "sibling"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	manifest := "require example.com/a v1.0.0\nreplace example.com/b => example.com/fork v1.2.3\n" +
		"replace example.com/fork => ./third_party/fork\n" +
		"replace example.com/missing => ./nowhere\n" +
		"replace example.com/sibling => ../sibling\n" +
		"replace example.com/abs => " + filepath.Join(parent, "sibling") + "\n" +
		"replace example.com/link => ./link\n"
	// the fork directory declares example.com/fork, so it is the one local entry
	got := LocalReplaces([]byte(manifest), root, root)
	if !reflect.DeepEqual(got, map[string]bool{"example.com/fork": true}) {
		t.Fatalf("LocalReplaces = %v, want only example.com/fork", got)
	}
	other := "replace example.com/fork => ./third_party/other\nreplace example.com/e => ./empty\n"
	if got := LocalReplaces([]byte(other), root, root); len(got) != 0 {
		t.Fatalf("a directory of another module or without a module line is not local: %v", got)
	}
}

// Boundary: an empty manifest, a leading byte-order mark, and a line past the line bound.
func TestLocalReplaces_Boundary_EmptyBOMAndLineBound(t *testing.T) {
	root := t.TempDir()
	writeModule(t, filepath.Join(root, "a"), "example.com/a")
	writeModule(t, filepath.Join(root, "last"), "example.com/last")
	if got := LocalReplaces(nil, root, root); len(got) != 0 {
		t.Fatalf("empty manifest: %v", got)
	}
	if got := LocalReplaces([]byte("\xef\xbb\xbfreplace example.com/a => ./a\n"), root, root); !got["example.com/a"] {
		t.Fatalf("a replace on the first line behind a byte-order mark is read, got %v", got)
	}
	last := "replace example.com/last => ./last\n"
	if got := LocalReplaces([]byte(strings.Repeat("\n", maxManifestLines-1)+last), root, root); !got["example.com/last"] {
		t.Fatalf("a replace on the last line inside the bound is read, got %v", got)
	}
	if got := LocalReplaces([]byte(strings.Repeat("\n", maxManifestLines)+last), root, root); len(got) != 0 {
		t.Fatalf("a replace past the line bound is not read, got %v", got)
	}
}
