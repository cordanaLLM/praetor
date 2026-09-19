package util

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadConfinedLimited_Positive(t *testing.T) {
	root := t.TempDir()
	writeLimitedFixture(t, filepath.Join(root, "cfg.yml"), "a: 1\n")
	data, err := ReadConfinedLimited(root, "cfg.yml", 1<<20)
	if err != nil {
		t.Fatalf("read a small file below the root: %v", err)
	}
	if string(data) != "a: 1\n" {
		t.Errorf("content = %q, want %q", data, "a: 1\n")
	}

	// A file of exactly the limit is content, not an overrun.
	writeLimitedFixture(t, filepath.Join(root, "exact.txt"), strings.Repeat("x", 64))
	data, err = ReadConfinedLimited(root, "exact.txt", 64)
	if err != nil || len(data) != 64 {
		t.Errorf("a file of exactly the limit must be read: %d bytes, err %v", len(data), err)
	}
}

func TestReadConfinedLimited_Negative(t *testing.T) {
	root := t.TempDir()
	writeLimitedFixture(t, filepath.Join(root, "cfg.yml"), "a: 1\n")
	// The escape target exists and is readable, so a refusal can only come from
	// confinement rather than from the file not being there.
	writeLimitedFixture(t, filepath.Join(root, "..", "outside.txt"), "{}")

	if _, err := ReadConfinedLimited(root, filepath.Join("..", "outside.txt"), 1<<10); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("a readable file outside the root must be refused by confinement, got %v", err)
	}
	if _, err := ReadConfinedLimited(root, "absent.yml", 1<<10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an absent file must report ErrNotExist, got %v", err)
	}
	if _, err := ReadConfinedLimited(root, "cfg.yml", 0); !errors.Is(err, ErrInvalidReadLimit) {
		t.Errorf("a zero limit must be refused, got %v", err)
	}
	if _, err := ReadConfinedLimited(root, "cfg.yml", -1); !errors.Is(err, ErrInvalidReadLimit) {
		t.Errorf("a negative limit must be refused, got %v", err)
	}
	if _, err := ReadConfinedLimited("", "cfg.yml", 1<<10); !errors.Is(err, ErrEmptyRoot) {
		t.Errorf("an empty root must be refused, got %v", err)
	}
}

func TestReadConfinedLimited_Boundary(t *testing.T) {
	root := t.TempDir()
	writeLimitedFixture(t, filepath.Join(root, "one-over.txt"), strings.Repeat("x", 65))
	if _, err := ReadConfinedLimited(root, "one-over.txt", 64); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("one byte past the limit must report ErrFileTooLarge, got %v", err)
	}

	// A directory where a file is required reads as an error, not as a crash.
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := ReadConfinedLimited(root, "dir", 1<<10); err == nil {
		t.Errorf("a directory is not a readable file")
	}
}

// TestReadConfinedLimited_Boundary_OversizeIsNotAllocated makes the assertion the verdict
// cannot make. A limit tested after os.ReadFile returns exactly the same verdict as one
// enforced by the read, so only the allocation tells the two apart: a bounded read touches
// about the limit, an unbounded one the whole file. The fixture is sized with os.Truncate,
// so it costs no disk on a filesystem with sparse files and is skipped where it cannot be
// created at all (HISS-21).
func TestReadConfinedLimited_Boundary_OversizeIsNotAllocated(t *testing.T) {
	const (
		oversize = int64(64 << 20)
		limit    = int64(1 << 20)
	)
	root := t.TempDir()
	path := filepath.Join(root, "huge.yml")
	writeLimitedFixture(t, path, "")
	if err := os.Truncate(path, oversize); err != nil {
		t.Skipf("this filesystem cannot size a file to %d bytes: %v", oversize, err)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := ReadConfinedLimited(root, "huge.yml", limit)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("a file past the limit must be refused, got %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(oversize/4) {
		t.Fatalf("reading a %d byte file allocated %d bytes: the limit bounds the verdict, not the read",
			oversize, allocated)
	}
}

func writeLimitedFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
