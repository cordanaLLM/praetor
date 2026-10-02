// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// splitFixture is a directory holding sub/file.md, and nothing else.
func splitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "file.md"), []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSplitAtExistingDir_Positive(t *testing.T) {
	root := splitFixture(t)
	sub := filepath.Join(root, "sub")
	for name, tc := range map[string]struct {
		path string
		want DirSplit
	}{
		"a directory is its own split":   {sub, DirSplit{Dir: sub, Exists: true}},
		"a file splits at its directory": {filepath.Join(sub, "file.md"), DirSplit{Dir: sub, Rest: "file.md", Exists: true}},
		"a missing file":                 {filepath.Join(sub, "gone.md"), DirSplit{Dir: sub, Rest: "gone.md"}},
		"a missing directory chain":      {filepath.Join(sub, "a", "b", "gone.md"), DirSplit{Dir: sub, Rest: filepath.Join("a", "b", "gone.md")}},
		"an unclean spelling is cleaned": {sub + string(os.PathSeparator) + "." + string(os.PathSeparator) + "a" + string(os.PathSeparator), DirSplit{Dir: sub, Rest: "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := SplitAtExistingDir(tc.path)
			if err != nil || got != tc.want {
				t.Fatalf("SplitAtExistingDir(%q) = %+v, %v; want %+v", tc.path, got, err, tc.want)
			}
			// The split loses nothing: Dir and Rest name the cleaned path again.
			if joined := filepath.Join(got.Dir, got.Rest); joined != filepath.Clean(tc.path) {
				t.Fatalf("Dir and Rest join to %q, want %q", joined, filepath.Clean(tc.path))
			}
		})
	}
}

func TestSplitAtExistingDir_Negative(t *testing.T) {
	root := splitFixture(t)
	// A name the host cannot look up is an unanswered question, never a missing path.
	invalid := filepath.Join(root, "sub", "nul\x00name", "x.md")
	if got, err := SplitAtExistingDir(invalid); err == nil || !strings.Contains(err.Error(), "util: inspect") {
		t.Fatalf("a stat failure other than absence must be an error: %+v, %v", got, err)
	}
	// More missing levels than the walk bound: refused, not walked without limit.
	deep := filepath.Join(root, strings.Repeat("d"+string(os.PathSeparator), maxPathAncestorWalk)+"x.md")
	if got, err := SplitAtExistingDir(deep); err == nil || !strings.Contains(err.Error(), "ancestor walk bound") {
		t.Fatalf("a path above the walk bound must be an error: %+v, %v", got, err)
	}
}

func TestSplitAtExistingDir_Boundary(t *testing.T) {
	root := splitFixture(t)
	sub := filepath.Join(root, "sub")
	// A path that runs through a regular file: POSIX answers ENOTDIR, Windows a missing path.
	// Either way the file ends the directory part and the split is the same.
	through := filepath.Join(sub, "file.md", "below", "x.md")
	want := DirSplit{Dir: sub, Rest: filepath.Join("file.md", "below", "x.md")}
	if got, err := SplitAtExistingDir(through); err != nil || got != want {
		t.Fatalf("through a file: %+v, %v; want %+v", got, err, want)
	}
	// The empty path is the current directory, and a relative path splits relative to it.
	t.Chdir(root)
	for path, want := range map[string]DirSplit{
		"":                   {Dir: ".", Exists: true},
		"gone/x.md":          {Dir: ".", Rest: filepath.Join("gone", "x.md")},
		"sub/gone/x.md":      {Dir: "sub", Rest: filepath.Join("gone", "x.md")},
		"sub/../sub/file.md": {Dir: "sub", Rest: "file.md", Exists: true},
	} {
		if got, err := SplitAtExistingDir(filepath.FromSlash(path)); err != nil || got != want {
			t.Errorf("SplitAtExistingDir(%q) = %+v, %v; want %+v", path, got, err, want)
		}
	}
	// The deepest walk the bound allows still answers: maxPathAncestorWalk-1 missing levels.
	deep := strings.Repeat("d"+string(os.PathSeparator), maxPathAncestorWalk-2) + "x.md"
	got, err := SplitAtExistingDir(filepath.Join("sub", deep))
	if err != nil || got.Dir != "sub" || got.Rest != deep || got.Exists {
		t.Fatalf("deepest allowed walk: dir=%q exists=%v err=%v", got.Dir, got.Exists, err)
	}
}

// TestSplitAtExistingDir_Symlinks: a link to a directory is a directory, a dangling link is
// missing, which is how os.Stat reads them.
func TestSplitAtExistingDir_Symlinks(t *testing.T) {
	root := splitFixture(t)
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "sub"), link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]DirSplit{
		filepath.Join(link, "gone.md"):     {Dir: link, Rest: "gone.md"},
		filepath.Join(dangling, "gone.md"): {Dir: root, Rest: filepath.Join("dangling", "gone.md")},
	} {
		if got, err := SplitAtExistingDir(path); err != nil || got != want {
			t.Errorf("SplitAtExistingDir(%q) = %+v, %v; want %+v", path, got, err, want)
		}
	}
}
