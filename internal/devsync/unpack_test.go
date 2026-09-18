package devsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// craftedEntry is one header of a hand-built archive.
type craftedEntry struct {
	name, body, link string
	kind             byte
}

func craftArchive(t *testing.T, entries ...craftedEntry) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.kind, Linkname: entry.link, Mode: 0o644, Size: int64(len(entry.body))}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	return &buffer
}

func file(name, body string) craftedEntry {
	return craftedEntry{name: name, body: body, kind: tar.TypeReg}
}
func symlink(name, target string) craftedEntry {
	return craftedEntry{name: name, link: target, kind: tar.TypeSymlink}
}

func TestExtractArchivePositive(t *testing.T) {
	requireSymlinks(t)
	target := t.TempDir()
	archive := craftArchive(t,
		craftedEntry{name: "docs/", kind: tar.TypeDir},
		file("README.md", "readme"),
		symlink("docs/README.md", "../README.md"),
		symlink("self", "."),
	)
	if err := extractArchive(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, filepath.Join(target, "docs", "README.md")) != "readme" {
		t.Fatal("link inside the target does not resolve")
	}
}

func TestExtractArchiveRefusesEscapes(t *testing.T) {
	cases := map[string][]craftedEntry{
		"parent name":       {file("../escaped", "x")},
		"absolute name":     {file("/tmp/escaped", "x")},
		"inner parent":      {file("a/../b", "x")},
		"link to parent":    {symlink("up", "..")},
		"link absolute":     {symlink("abs", "/etc/passwd")},
		"link deep parent":  {symlink("a/b", "../../x")},
		"link through link": {symlink("a/d", ".."), symlink("l", "a/d/..")},
		"link under link":   {symlink("l", "."), symlink("l/x", "../y")},
		"hard link":         {{name: "h", link: "README.md", kind: tar.TypeLink}},
		"duplicate file":    {file("same", "1"), file("same", "2")},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := extractArchive(context.Background(), craftArchive(t, entries...), target); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			for _, leaked := range []string{"escaped", "x", "y", "b"} {
				if _, err := os.Lstat(filepath.Join(parent, leaked)); err == nil {
					t.Fatalf("%s written outside the target", leaked)
				}
			}
		})
	}
}

func TestExtractArchiveBoundary(t *testing.T) {
	if err := extractArchive(context.Background(), craftArchive(t), t.TempDir()); err != nil {
		t.Fatalf("empty archive rejected: %v", err)
	}
	whole := craftArchive(t, file("a.txt", strings.Repeat("a", 4096))).Bytes()
	truncated := bytes.NewReader(whole[:len(whole)-8])
	if err := extractArchive(context.Background(), truncated, t.TempDir()); err == nil {
		t.Fatal("truncated archive accepted")
	}
	if err := extractArchive(context.Background(), strings.NewReader("not gzip"), t.TempDir()); err == nil {
		t.Fatal("non-gzip stream accepted")
	}
}

func TestLinkStaysInside(t *testing.T) {
	links := map[string]bool{"lnk": true, "a/inner": true}
	cases := []struct {
		name, target string
		want         bool
	}{
		{"a/b", "../c", true},
		{"a/b", "c/d", true},
		{"top", ".", true},
		{"a/b", "../../c", false},
		{"top", "", false},
		{"top", "/abs", false},
		{"a/b", "inner/x", false},
		{"lnk/x", "y", false},
		{"a/b", strings.Repeat("x/", maxLinkSegments+1), false},
	}
	for _, c := range cases {
		if got := linkStaysInside(c.name, c.target, links); got != c.want {
			t.Errorf("linkStaysInside(%q, %q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

func TestEntryName(t *testing.T) {
	if name, err := entryName("dir/file.txt"); err != nil || name != filepath.Join("dir", "file.txt") {
		t.Fatalf("entryName = %q, %v", name, err)
	}
	if name, err := entryName("dir/"); err != nil || name != "dir" {
		t.Fatalf("directory entry = %q, %v", name, err)
	}
	for _, bad := range []string{"", "/", "..", "a/../b", "/abs"} {
		if _, err := entryName(bad); !errors.Is(err, ErrUnsafeEntry) {
			t.Errorf("entryName(%q) accepted", bad)
		}
	}
}
