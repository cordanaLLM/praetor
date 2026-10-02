package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// caseInsensitiveDir reports whether the file system below dir ignores letter case, by asking
// for a file it just wrote under another spelling.
func caseInsensitiveDir(t *testing.T, dir string) bool {
	t.Helper()
	writeFixtureFile(t, dir, "CaseProbe.tmp", "probe\n")
	_, err := os.Stat(filepath.Join(dir, "caseprobe.TMP"))
	if removeErr := os.Remove(filepath.Join(dir, "CaseProbe.tmp")); removeErr != nil {
		t.Fatalf("remove the case probe: %v", removeErr)
	}
	return err == nil
}

// TestCavemanCompressHandlesEachFileOnce pins the review finding on #368: a file reached
// twice is one input. Every input was read before the first write, so the second write of one
// file failed its compare-and-swap ("snapshot changed before replacement") and stopped the
// run in the middle of the list.
func TestCavemanCompressHandlesEachFileOnce(t *testing.T) {
	for name, args := range map[string]func(dir, loose string) []string{
		"named twice":         func(_, loose string) []string { return []string{loose, loose} },
		"directory then file": func(dir, loose string) []string { return []string{dir, loose} },
		"file then directory": func(dir, loose string) []string { return []string{loose, dir} },
		"unclean spelling": func(dir, loose string) []string {
			sep := string(filepath.Separator)
			return []string{loose, dir + sep + "sub" + sep + ".." + sep + "loose.md"}
		},
		"relative and absolute":  func(_, loose string) []string { return []string{"loose.md", loose} },
		"dot and plain relative": func(_, _ string) []string { return []string{"loose.md", "." + string(filepath.Separator) + "loose.md"} },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
			writeFixtureFile(t, dir, "sub/keep.txt", "not an input\n")
			t.Chdir(dir)
			out, err := runCavemanCLI(t, "", append([]string{"compress", "--in-place"}, args(dir, loose)...)...)
			if err != nil {
				t.Fatalf("a file reached twice must be compressed once: %v\n%s", err, out)
			}
			if strings.Count(out, "status=rewritten") != 1 || !strings.Contains(out, "total: inputs=1 bytes=68->53 (-15) tokens_est=10->10 (+0) rewritten=1 unchanged=0 refused=0") {
				t.Fatalf("want one row and a total of one input:\n%s", out)
			}
			if got := readFixtureFile(t, dir, "loose.md"); got != compressTight {
				t.Fatalf("the file must be rewritten: %q", got)
			}
		})
	}
}

// TestCavemanCompressHandlesEachFileOnceNegative: only a repeated file is dropped. Two files
// stay two inputs, in argument order, in every mode, and a file later in the list is still
// handled after a repeated one.
func TestCavemanCompressHandlesEachFileOnceNegative(t *testing.T) {
	dir := t.TempDir()
	first := writeFixtureFile(t, dir, "a/first.md", compressLoose)
	second := writeFixtureFile(t, dir, "b/second.md", compressLoose)
	out, err := runCavemanCLI(t, "", "compress", "--stats", second, first, second, first)
	if err != nil || strings.Count(out, "status=would-change") != 2 || !strings.Contains(out, "total: inputs=2 ") {
		t.Fatalf("--stats: err=%v\n%s", err, out)
	}
	if strings.Index(out, "second.md") > strings.Index(out, "first.md") {
		t.Fatalf("the first spelling of each file decides the order:\n%s", out)
	}
	out, err = runCavemanCLI(t, "", "compress", "--in-place", first, first, second)
	if err != nil || strings.Count(out, "status=rewritten") != 2 || !strings.Contains(out, "total: inputs=2 ") {
		t.Fatalf("--in-place: err=%v\n%s", err, out)
	}
	if readFixtureFile(t, dir, "a/first.md") != compressTight || readFixtureFile(t, dir, "b/second.md") != compressTight {
		t.Fatal("the file after a repeated one was not rewritten")
	}
	// Two inputs stay too many for standard output; one input named twice is one.
	if _, err = runCavemanCLI(t, "", "compress", first, second); err == nil || !strings.Contains(err.Error(), "2 inputs") {
		t.Fatalf("two files to standard output: %v", err)
	}
	if out, err = runCavemanCLI(t, "", "compress", first, first); err != nil || out != compressTight {
		t.Fatalf("one file named twice to standard output: err=%v\n%q", err, out)
	}
}

// TestCavemanCompressHandlesEachFileOnceBoundary: hard links are two names and both are
// rewritten, since the atomic write replaces one name; standard input is no file and is never
// dropped; and where the file system ignores case, two spellings of one file are one input.
func TestCavemanCompressHandlesEachFileOnceBoundary(t *testing.T) {
	dir := t.TempDir()
	loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
	out, err := runCavemanCLI(t, compressLoose, "compress", "--stats", "-", loose, "-")
	if err != nil || !strings.Contains(out, "total: inputs=3 ") {
		t.Fatalf("standard input beside a file: err=%v\n%s", err, out)
	}
	t.Run("hard links", func(t *testing.T) {
		linked := filepath.Join(dir, "linked.md")
		if err := os.Link(loose, linked); err != nil {
			t.Skipf("hard links are unavailable here: %v", err)
		}
		out, err := runCavemanCLI(t, "", "compress", "--in-place", loose, linked)
		if err != nil || strings.Count(out, "status=rewritten") != 2 {
			t.Fatalf("both names must be rewritten: err=%v\n%s", err, out)
		}
		if readFixtureFile(t, dir, "loose.md") != compressTight || readFixtureFile(t, dir, "linked.md") != compressTight {
			t.Fatal("a hard-linked name kept the old text")
		}
	})
	t.Run("spellings that differ in case", func(t *testing.T) {
		caseDir := t.TempDir()
		if !caseInsensitiveDir(t, caseDir) {
			t.Skip("this file system distinguishes letter case: LOOSE.md and loose.md are two files here")
		}
		lower := writeFixtureFile(t, caseDir, "loose.md", compressLoose)
		out, err := runCavemanCLI(t, "", "compress", "--in-place", lower, filepath.Join(caseDir, "LOOSE.md"))
		if err != nil || strings.Count(out, "status=rewritten") != 1 || !strings.Contains(out, "total: inputs=1 ") {
			t.Fatalf("two spellings of one file must be one input: err=%v\n%s", err, out)
		}
	})
}

// TestCavemanCompressKeepsFileMode pins the review finding on #368: an in-place rewrite keeps
// the mode the file had. The writer's 0644 ceiling used to turn a 0755 file into a 0644 one,
// which git records as a mode change beside the text change.
func TestCavemanCompressKeepsFileMode(t *testing.T) {
	if !util.ModeIsProtection() {
		t.Skip("this host stores no permission bits beyond read-only, so there is no execute bit to keep")
	}
	for _, mode := range []os.FileMode{0o755, 0o644, 0o600, 0o444, 0o711} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			dir := t.TempDir()
			loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
			tight := writeFixtureFile(t, dir, "tight.md", compressTight)
			for _, path := range []string{loose, tight} {
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runCavemanCLI(t, "", "compress", "--in-place", loose, tight)
			if err != nil || !strings.Contains(out, "status=rewritten") || !strings.Contains(out, "status=unchanged") {
				t.Fatalf("err=%v\n%s", err, out)
			}
			if got := readFixtureFile(t, dir, "loose.md"); got != compressTight {
				t.Fatalf("not rewritten: %q", got)
			}
			for _, path := range []string{loose, tight} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Errorf("%s: mode %o became %v (%v)", filepath.Base(path), mode, info.Mode().Perm(), err)
				}
			}
		})
	}
}

// TestCavemanCompressInPlaceIsMarkdownOnly pins the review finding on #368: --in-place rewrote
// any named file through the Markdown cleanup, so `x = "a  b"` in a Python file lost a blank.
// A file that is not Markdown is refused by name, before any file is written.
func TestCavemanCompressInPlaceIsMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	python := "x = \"a  b\"\ncol1\t\tcol3\n"
	script := writeFixtureFile(t, dir, "t.py", python)
	bare := writeFixtureFile(t, dir, "README", compressLoose)
	loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
	for name, tc := range map[string]struct {
		args []string
		want []string
	}{
		"one file":              {[]string{script}, []string{"--in-place rewrites .md files only, not ", "/t.py; nothing written"}},
		"after a Markdown file": {[]string{loose, script}, []string{"/t.py; nothing written"}},
		"no extension":          {[]string{bare}, []string{"/README; nothing written"}},
		"each one is named":     {[]string{script, loose, bare}, []string{"/t.py, ", "/README; nothing written"}},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", append([]string{"compress", "--in-place"}, tc.args...)...)
			if err == nil || out != "" {
				t.Fatalf("want a refusal and no report: err=%v\n%s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "loose.md") {
				t.Errorf("a Markdown file was named as refused: %v", err)
			}
		})
	}
	if readFixtureFile(t, dir, "t.py") != python || readFixtureFile(t, dir, "README") != compressLoose || readFixtureFile(t, dir, "loose.md") != compressLoose {
		t.Fatal("a refused request wrote a file")
	}

	// The modes that write no file take any text, as they did.
	if out, err := runCavemanCLI(t, "", "compress", script); err != nil || out != "x = \"a  b\"\ncol1 col3\n" {
		t.Fatalf("standard output: err=%v\n%q", err, out)
	}
	if out, err := runCavemanCLI(t, "", "compress", "--stats", script, bare); err != nil || !strings.Contains(out, "total: inputs=2 ") {
		t.Fatalf("--stats: err=%v\n%s", err, out)
	}
	if out, err := runCavemanCLI(t, python, "compress", "-"); err != nil || out != "x = \"a  b\"\ncol1 col3\n" {
		t.Fatalf("standard input: err=%v\n%q", err, out)
	}
}

// TestCavemanCompressInPlaceIsMarkdownOnlyBoundary: the extension is matched whatever its
// case, as the directory expansion matches it, and a long refusal names a bounded number of
// files and counts the rest.
func TestCavemanCompressInPlaceIsMarkdownOnlyBoundary(t *testing.T) {
	dir := t.TempDir()
	upper := writeFixtureFile(t, dir, "UPPER.MD", compressLoose)
	out, err := runCavemanCLI(t, "", "compress", "--in-place", upper)
	if err != nil || !strings.Contains(out, "status=rewritten") || readFixtureFile(t, dir, "UPPER.MD") != compressTight {
		t.Fatalf("an upper-case extension is Markdown: err=%v\n%s", err, out)
	}
	for count, want := range map[int]string{
		maxNamedRefusals:     "/f7.txt; nothing written",
		maxNamedRefusals + 1: "/f7.txt (+1 more); nothing written",
		maxNamedRefusals + 3: "/f7.txt (+3 more); nothing written",
	} {
		args := []string{"compress", "--in-place"}
		for i := range count {
			args = append(args, writeFixtureFile(t, dir, fmt.Sprintf("f%d.txt", i), "a  b\n"))
		}
		if _, err = runCavemanCLI(t, "", args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d files: want an error holding %q: %v", count, want, err)
		}
	}
}

// TestCavemanCompressKeepsMarkdownMeaning runs the two Markdown constructs the review found
// Compress changing through the command: an indented code block and a hard line break keep
// their bytes while the prose beside them is cleaned (#368; the rule itself is pinned in
// internal/caveman/compress_test.go).
func TestCavemanCompressKeepsMarkdownMeaning(t *testing.T) {
	const (
		kept  = "Title\n\n    key:    value   # aligned\n\n\n    second   chunk\n\nline one  \nline two\\\nline three\n"
		loose = "Title   text  \n\n\n\n    key:    value   # aligned\n\nline one  \nline   two\n"
		tight = "Title text\n\n    key:    value   # aligned\n\nline one  \nline two\n"
	)
	dir := t.TempDir()
	keptPath := writeFixtureFile(t, dir, "kept.md", kept)
	loosePath := writeFixtureFile(t, dir, "loose.md", loose)
	out, err := runCavemanCLI(t, "", "compress", "--in-place", keptPath, loosePath)
	if err != nil || !strings.Contains(out, "kept.md: bytes=91->91 (+0) lines=10->10 (+0) tokens_est=16->16 (+0) status=unchanged") ||
		!strings.Contains(out, "loose.md: bytes=71->63 (-8) lines=8->6 (-2) tokens_est=13->13 (+0) status=rewritten") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	if got := readFixtureFile(t, dir, "kept.md"); got != kept {
		t.Fatalf("code or a hard line break changed: %q", got)
	}
	if got := readFixtureFile(t, dir, "loose.md"); got != tight {
		t.Fatalf("loose.md = %q, want %q", got, tight)
	}
	// The same indentation that continues a paragraph is prose, and is cleaned.
	if out, err = runCavemanCLI(t, "para\n    lazy   line  \n", "compress", "-"); err != nil || out != "para\n    lazy line\n" {
		t.Fatalf("paragraph continuation: err=%v\n%q", err, out)
	}
}
