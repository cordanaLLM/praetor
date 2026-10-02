package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

const (
	// compressLoose carries every cleanup Compress applies: a CRLF line end, an ANSI escape,
	// an inner blank run, trailing blanks and a run of blank lines. Its fence must keep its
	// double blank.
	compressLoose = "verdict:   pass\r\n\x1b[1mchanged:\x1b[m none   \n\n\n\n```text\nkeep   this\n```\n"
	compressTight = "verdict: pass\nchanged: none\n\n```text\nkeep   this\n```\n"
	// compressFolding repeats a directive line. Compress folds the pair into one line ending
	// in (x2), which drops one MUST: the clarity floor (F6) refuses that.
	compressFolding = "MUST run `make verify-all`.\nMUST run `make verify-all`.\n"
)

// TestCavemanCompressPositive: the default prints the compressed text of one input and leaves
// the file alone; standard input works the same way; --stats measures without writing; and
// --in-place rewrites a file that changes, walking a directory the way check does.
func TestCavemanCompressPositive(t *testing.T) {
	dir := t.TempDir()
	loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
	out, err := runCavemanCLI(t, "", "compress", loose)
	if err != nil || out != compressTight {
		t.Fatalf("default must print the compressed text and nothing else: err=%v\n%q", err, out)
	}
	if got := readFixtureFile(t, dir, "loose.md"); got != compressLoose {
		t.Fatalf("default must not touch the file: %q", got)
	}
	if out, err = runCavemanCLI(t, compressLoose, "compress", "-"); err != nil || out != compressTight {
		t.Fatalf("stdin: err=%v\n%q", err, out)
	}

	out, err = runCavemanCLI(t, "", "compress", "--stats", loose)
	if err != nil || !strings.Contains(out, "loose.md: bytes=68->53 (-15) lines=8->6 (-2) tokens_est=10->10 (+0) status=would-change") {
		t.Fatalf("--stats row: err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "total: inputs=1 bytes=68->53 (-15) tokens_est=10->10 (+0) would-change=1 unchanged=0 refused=0") {
		t.Fatalf("--stats total:\n%s", out)
	}
	if got := readFixtureFile(t, dir, "loose.md"); got != compressLoose {
		t.Fatalf("--stats must not touch the file: %q", got)
	}

	// A directory expands to its Markdown files; the text file beside them is not an input.
	writeFixtureFile(t, dir, "nested/tight.md", compressTight)
	writeFixtureFile(t, dir, "nested/notes.txt", compressLoose)
	out, err = runCavemanCLI(t, "", "compress", "--in-place", dir)
	if err != nil {
		t.Fatalf("--in-place: err=%v\n%s", err, out)
	}
	for _, want := range []string{
		"loose.md: bytes=68->53 (-15) lines=8->6 (-2) tokens_est=10->10 (+0) status=rewritten",
		"nested/tight.md: bytes=53->53 (+0) lines=6->6 (+0) tokens_est=10->10 (+0) status=unchanged",
		"total: inputs=2 bytes=121->106 (-15) tokens_est=20->20 (+0) rewritten=1 unchanged=1 refused=0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--in-place report lacks %q:\n%s", want, out)
		}
	}
	if got := readFixtureFile(t, dir, "loose.md"); got != compressTight {
		t.Fatalf("--in-place must rewrite the file: %q", got)
	}
	if got := readFixtureFile(t, dir, "nested/notes.txt"); got != compressLoose {
		t.Fatalf("a file outside the Markdown expansion was rewritten: %q", got)
	}
}

// TestCavemanCompressAgreesWithEstimate: compress and estimate print one input's bytes, lines
// and estimated tokens from the same measure (#368 item 3).
func TestCavemanCompressAgreesWithEstimate(t *testing.T) {
	dir := t.TempDir()
	loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
	estimate, err := runCavemanCLI(t, "", "estimate", loose)
	if err != nil || !strings.Contains(estimate, "loose.md: bytes=68 lines=8 tokens_est=10\n") {
		t.Fatalf("estimate: err=%v\n%s", err, estimate)
	}
	stats, err := runCavemanCLI(t, "", "compress", "--stats", loose)
	if err != nil || !strings.Contains(stats, "loose.md: bytes=68->") || !strings.Contains(stats, " lines=8->") || !strings.Contains(stats, " tokens_est=10->") {
		t.Fatalf("compress --stats must open each figure with estimate's: err=%v\n%s", err, stats)
	}
}

// TestCavemanCompressRefusesWhatTheFloorHolds: a compression that fails caveman.Floor is never
// printed or written, in any mode, and the command exits non-zero naming the lost fact. A
// refused file does not stop the files beside it.
func TestCavemanCompressRefusesWhatTheFloorHolds(t *testing.T) {
	dir := t.TempDir()
	folding := writeFixtureFile(t, dir, "a-folding.md", compressFolding)
	writeFixtureFile(t, dir, "b-loose.md", compressLoose)

	out, err := runCavemanCLI(t, "", "compress", folding)
	if err == nil || out != "" || !strings.Contains(err.Error(), "a-folding.md refused") || !strings.Contains(err.Error(), "F6 must-dropped: 2 -> 1") {
		t.Fatalf("default must refuse and print no text: err=%v\n%q", err, out)
	}
	if out, err = runCavemanCLI(t, compressFolding, "compress", "-"); err == nil || out != "" {
		t.Fatalf("stdin must be refused the same way: err=%v\n%q", err, out)
	}

	out, err = runCavemanCLI(t, "", "compress", "--stats", dir)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 input(s) refused") {
		t.Fatalf("--stats must fail on a refused input: err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "b-loose.md: bytes=68->53 (-15)") || !strings.Contains(out, "status=would-change") {
		t.Fatalf("--stats must still measure the input beside the refused one:\n%s", out)
	}

	out, err = runCavemanCLI(t, "", "compress", "--in-place", dir)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 input(s) refused") {
		t.Fatalf("--in-place must fail on a refused input: err=%v\n%s", err, out)
	}
	for _, want := range []string{
		"a-folding.md: bytes=56->56 (+0) lines=2->2 (+0) tokens_est=10->10 (+0) status=refused",
		"a-folding.md:0 F6 must-dropped: 2 -> 1",
		"status=rewritten",
		"total: inputs=2 bytes=124->109 (-15) tokens_est=20->20 (+0) rewritten=1 unchanged=0 refused=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--in-place report lacks %q:\n%s", want, out)
		}
	}
	if got := readFixtureFile(t, dir, "a-folding.md"); got != compressFolding {
		t.Fatalf("a refused file was rewritten: %q", got)
	}
	if got := readFixtureFile(t, dir, "b-loose.md"); got != compressTight {
		t.Fatalf("the file beside a refused one must still be rewritten: %q", got)
	}
}

func TestCavemanCompressNegative(t *testing.T) {
	dir := t.TempDir()
	loose := writeFixtureFile(t, dir, "loose.md", compressLoose)
	other := writeFixtureFile(t, dir, "other.md", compressTight)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no input":              {[]string{"compress"}, "usage: praetorctl caveman"},
		"missing file":          {[]string{"compress", filepath.Join(dir, "absent.md")}, "absent.md"},
		"missing file in place": {[]string{"compress", "--in-place", filepath.Join(dir, "absent.md")}, "absent.md"},
		"unknown flag":          {[]string{"compress", "--write", loose}, "flag provided but not defined"},
		"stdin in place":        {[]string{"compress", "--in-place", "-"}, "--in-place cannot rewrite standard input"},
		"stdin beside a file":   {[]string{"compress", "--in-place", loose, "-"}, "--in-place cannot rewrite standard input"},
		"both modes":            {[]string{"compress", "--stats", "--in-place", loose}, "pass one of them"},
		"two inputs to stdout":  {[]string{"compress", loose, other}, "2 inputs, and standard output holds the text of one"},
		"directory to stdout":   {[]string{"compress", dir}, "2 inputs, and standard output holds the text of one"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
				t.Fatalf("want an error holding %q and no output: err=%v\n%q", tc.want, err, out)
			}
		})
	}
	if got := readFixtureFile(t, dir, "loose.md"); got != compressLoose {
		t.Fatalf("a refused request rewrote a file: %q", got)
	}
}

func TestCavemanCompressBoundary(t *testing.T) {
	dir := t.TempDir()
	// An empty file and an already compressed one are no change, and no change is a success.
	empty := writeFixtureFile(t, dir, "empty/empty.md", "")
	if out, err := runCavemanCLI(t, "", "compress", empty); err != nil || out != "" {
		t.Fatalf("empty file: err=%v\n%q", err, out)
	}
	out, err := runCavemanCLI(t, "", "compress", "--in-place", empty)
	if err != nil || !strings.Contains(out, "empty.md: bytes=0->0 (+0) lines=0->0 (+0) tokens_est=0->0 (+0) status=unchanged") {
		t.Fatalf("empty file in place: err=%v\n%s", err, out)
	}
	tight := writeFixtureFile(t, dir, "tight/tight.md", compressTight)
	if out, err = runCavemanCLI(t, "", "compress", tight); err != nil || out != compressTight {
		t.Fatalf("compressing compressed text must return it unchanged: err=%v\n%q", err, out)
	}
	// A directory holding exactly one Markdown file is one input, so its text can be printed.
	if out, err = runCavemanCLI(t, "", "compress", filepath.Dir(tight)); err != nil || out != compressTight {
		t.Fatalf("one-file directory: err=%v\n%q", err, out)
	}
	// A text without a final newline keeps that; an ANSI escape holds digits but is no number
	// the floor would miss; and one prose line repeated without a directive folds.
	if out, err = runCavemanCLI(t, "\x1b[38;5;196msync ok\x1b[0m\nsync ok\nnext", "compress", "-"); err != nil || out != "sync ok (x2)\nnext" {
		t.Fatalf("fold, ANSI and missing final newline: err=%v\n%q", err, out)
	}
	// Input is bounded like check's: 1 MiB passes the reader, one byte more does not.
	if _, err = runCavemanCLI(t, strings.Repeat("a", 1<<20+1), "compress", "-"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("stdin above 1 MiB must be refused: %v", err)
	}
	if out, err = runCavemanCLI(t, strings.Repeat("a", 1<<20), "compress", "--stats", "-"); err != nil || !strings.Contains(out, "-: bytes=1048576->1048576 (+0)") {
		t.Fatalf("stdin of exactly 1 MiB must be measured: err=%v\n%s", err, out)
	}
}

// TestCavemanCompressRefusesFileEditedSinceRead: the in-place write is bound to the bytes that
// were read. A file that changed in between keeps its new content, the run stops with an error
// naming it, and the lines of the inputs handled before it are still printed.
func TestCavemanCompressRefusesFileEditedSinceRead(t *testing.T) {
	dir := t.TempDir()
	first := writeFixtureFile(t, dir, "first.md", compressLoose)
	edited := writeFixtureFile(t, dir, "edited.md", "edited after the read\n")
	inputs := []cavemanInput{
		{name: filepath.ToSlash(first), text: compressLoose},
		{name: filepath.ToSlash(edited), text: compressLoose},
	}
	var out bytes.Buffer
	err := reportCavemanCompressions(context.Background(), inputs, true, &out)
	if err == nil || !strings.Contains(err.Error(), "caveman compress: write "+filepath.ToSlash(edited)) {
		t.Fatalf("want a write error naming the edited file: %v\n%s", err, out.String())
	}
	if got := readFixtureFile(t, dir, "edited.md"); got != "edited after the read\n" {
		t.Fatalf("a file edited since the read was overwritten: %q", got)
	}
	if got := readFixtureFile(t, dir, "first.md"); got != compressTight {
		t.Fatalf("the file before the failure must be rewritten: %q", got)
	}
	if !strings.Contains(out.String(), "first.md: bytes=68->53 (-15)") || !strings.Contains(out.String(), "total: inputs=1 ") {
		t.Fatalf("the report must hold the inputs handled before the failure:\n%s", out.String())
	}
}

// TestCompressCavemanInputUsesTheLibrary: the command adds no cleanup of its own. Its text and
// figures are caveman.Compress's, and its verdict is caveman.Floor's.
func TestCompressCavemanInputUsesTheLibrary(t *testing.T) {
	for name, text := range map[string]string{"loose": compressLoose, "tight": compressTight, "folding": compressFolding, "empty": ""} {
		t.Run(name, func(t *testing.T) {
			got := compressCavemanInput(cavemanInput{name: name, text: text})
			wantText, wantStats := caveman.Compress(text)
			if got.text != wantText || got.stats != wantStats {
				t.Fatalf("text or stats differ from caveman.Compress: %q %+v", got.text, got.stats)
			}
			if got.floor.Passed() != caveman.Floor(caveman.StripANSI(text), wantText).Passed() {
				t.Fatalf("verdict differs from caveman.Floor")
			}
		})
	}
}
