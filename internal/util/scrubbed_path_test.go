package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestScrubbedToolPathIncludesTheToolDirectory is the positive case: the directory the tool
// was resolved in is always the first entry, so the subprocess can still find it.
func TestScrubbedToolPathIncludesTheToolDirectory(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "sub", "tool")
	entries := strings.Split(ScrubbedToolPath(tool), string(os.PathListSeparator))
	if entries[0] != filepath.Dir(tool) {
		t.Fatalf("first PATH entry = %q, want %q", entries[0], filepath.Dir(tool))
	}
}

// TestScrubbedToolPathOmitsAbsentPOSIXDirectories is the negative case: a candidate
// directory that does not exist is never named, which is the Windows failure the helper
// exists to avoid.
func TestScrubbedToolPathOmitsAbsentPOSIXDirectories(t *testing.T) {
	entries := strings.Split(ScrubbedToolPath(filepath.Join(t.TempDir(), "tool")), string(os.PathListSeparator))
	for _, entry := range entries[1:] {
		if info, err := os.Stat(entry); err != nil || !info.IsDir() {
			t.Fatalf("PATH names %q, which is not an existing directory", entry)
		}
	}
	if runtime.GOOS == "windows" && len(entries) != 1 {
		t.Fatalf("PATH = %v, want only the tool directory on a host without /usr/bin or /bin", entries)
	}
}

// TestScrubbedToolPathBoundsEachEntry is the boundary case: a bare tool name, whose
// directory is ".", still yields one non-empty entry per element and never a bare
// separator that a child would read as the current directory twice.
func TestScrubbedToolPathBoundsEachEntry(t *testing.T) {
	value := ScrubbedToolPath("tool")
	entries := strings.Split(value, string(os.PathListSeparator))
	if entries[0] != "." {
		t.Fatalf("first PATH entry = %q, want %q", entries[0], ".")
	}
	for _, entry := range entries {
		if entry == "" {
			t.Fatalf("PATH %q contains an empty entry", value)
		}
	}
	if len(entries) != len(posixToolDirectories)+1 && runtime.GOOS != "windows" {
		t.Logf("PATH %q omits a POSIX directory absent on this host", value)
	}
}
