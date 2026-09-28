package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSplitFilePath_Positive pins the split the confined writers anchor at: a relative, an
// absolute and a bare file name each split into the directory filepath.Dir names and the
// last element.
func TestSplitFilePath_Positive(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "report.md")
	cases := []struct{ path, dir, name string }{
		{filepath.Join("out", "report.md"), "out", "report.md"},
		{abs, filepath.Dir(abs), "report.md"},
		{"report.md", ".", "report.md"},
	}
	for _, tc := range cases {
		dir, name, err := SplitFilePath(tc.path)
		if err != nil || dir != tc.dir || name != tc.name {
			t.Errorf("SplitFilePath(%q) = (%q, %q, %v), want (%q, %q, nil)", tc.path, dir, name, err, tc.dir, tc.name)
		}
	}
}

// TestSplitFilePath_Negative pins the refusals: a path ending in a separator names a
// directory (filepath.Base would drop the separator and name the directory itself), and so
// do "." and "..", an empty path and a root.
func TestSplitFilePath_Negative(t *testing.T) {
	for _, path := range []string{"out" + string(filepath.Separator), ".", "..", filepath.Join("out", ".."), "", string(filepath.Separator)} {
		if _, _, err := SplitFilePath(path); !errors.Is(err, ErrDirectoryPath) {
			t.Errorf("SplitFilePath(%q) error = %v, want ErrDirectoryPath", path, err)
		}
	}
}

// TestWriteFileAt_Boundary pins the case the helper exists for: a directory-shaped output
// path writes nothing (the split would have written out/out), while the same path without
// the separator writes the file below its directory.
func TestWriteFileAt_Boundary(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAt(out+string(filepath.Separator), []byte("x"), SecureFilePerm); !errors.Is(err, ErrDirectoryPath) {
		t.Fatalf("WriteFileAt(out/) error = %v, want ErrDirectoryPath", err)
	}
	if _, err := os.Lstat(filepath.Join(out, "out")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WriteFileAt(out/) left out/out behind (lstat err %v)", err)
	}
	target := filepath.Join(out, "report.md")
	if err := WriteFileAt(target, []byte("kept"), SecureFilePerm); err != nil {
		t.Fatalf("WriteFileAt(%s): %v", target, err)
	}
	data, err := os.ReadFile(target) // #nosec G304 -- test-local path from t.TempDir
	if err != nil || string(data) != "kept" {
		t.Fatalf("written file = (%q, %v), want %q", data, err, "kept")
	}
}
