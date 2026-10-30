package util

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Positive: a file under the limit reads whole, and one over it reads as its first limit bytes,
// with no ErrFileTooLarge.
func TestReadFilePrefix_Positive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	for limit, want := range map[int64]string{20: "0123456789", 4: "0123"} {
		got, err := ReadFilePrefix(path, limit)
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Errorf("ReadFilePrefix(limit %d) = %q, %v; want %q", limit, got, err, want)
		}
	}
}

// Negative: a missing file and a directory are refused as ReadFileLimited refuses them.
func TestReadFilePrefix_Negative(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadFilePrefix(filepath.Join(dir, "absent"), 8); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := ReadFilePrefix(dir, 8); !errors.Is(err, ErrNotRegularFile) {
		t.Errorf("directory: %v", err)
	}
}

// Boundary: a limit equal to the file's size reads it whole, an empty file reads empty, and a
// non-positive limit is refused.
func TestReadFilePrefix_Boundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFilePrefix(path, 3); err != nil || string(got) != "abc" {
		t.Errorf("exact limit = %q, %v", got, err)
	}
	if _, err := ReadFilePrefix(path, 0); !errors.Is(err, ErrInvalidReadLimit) {
		t.Errorf("zero limit: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFilePrefix(empty, 8); err != nil || len(got) != 0 {
		t.Errorf("empty file = %q, %v", got, err)
	}
}
