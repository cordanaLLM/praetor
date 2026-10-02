package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// boundMessage is the error of a directory that holds more files to compare than one run reads.
var boundMessage = fmt.Sprintf("holds more than %d Markdown files that git tracks or would track", maxCavemanFiles)

// writeMarkdownFiles writes count one-word Markdown files below rel, spread over directories.
func writeMarkdownFiles(t *testing.T, dir, rel string, count int) {
	t.Helper()
	for i := range count {
		writeFixtureFile(t, dir, fmt.Sprintf("%s/%02d/f%04d.md", rel, i%32, i), "one\n")
	}
}

// TestCavemanEstimateBaseLeftAloneTreeAboveBound pins the major finding on #373 that the
// working-tree side walked the tree: Markdown that git leaves alone was counted against
// maxCavemanFiles before it was dropped, so a run at a repository root beside a ledger, a
// dependency tree or another checkout failed with "more than 4096 input files" although no such
// file was an input. The side is git's listing now. One more file than the bound below an
// ignored directory, and the same below a nested repository, change neither the rows nor the
// total of the root, and no file of either is counted.
func TestCavemanEstimateBaseLeftAloneTreeAboveBound(t *testing.T) {
	for name, rel := range map[string]string{
		"ignored directory": "ledger/checkouts",
		"nested repository": "docs/nested/pages",
	} {
		t.Run(name, func(t *testing.T) {
			dir, env := ignoreFixture(t)
			nested := filepath.Join(dir, "docs", "nested")
			if out, err := runFixtureGit(t, nested, env, "add", "-A"); err != nil {
				t.Fatalf("git add in the nested repository: %v (%s)", err, out)
			}
			if out, err := runFixtureGit(t, nested, env, "commit", "-q", "-m", "nested"); err != nil {
				t.Fatalf("git commit in the nested repository: %v (%s)", err, out)
			}
			writeMarkdownFiles(t, dir, rel, maxCavemanFiles+1)
			out, err := estimateBase(t, dir)
			if err != nil {
				t.Fatalf("a tree git leaves alone must not reach the bound: %v", err)
			}
			for _, left := range []string{"ledger/", "nested/", ".scratch.md: bytes=0->"} {
				if strings.Contains(out, left) {
					t.Errorf("a file below %s is a row:\n%.2000s", left, out)
				}
			}
			const total = "total: inputs=7 bytes=100->110 (+10) tokens_est=23->25 (+2) base_absent=1 worktree_absent=0 worktree_ignored=0\n"
			if !strings.Contains(out, "docs/untracked.md: bytes=0->10 (+10)") || !strings.HasSuffix(out, total) {
				t.Errorf("want the six tracked files and the one git would track:\n%.2000s", out)
			}
		})
	}
}

// TestBaselineWorktreeFilesBound covers the bound of the listed set. Boundary: exactly
// maxCavemanFiles files git would track are all returned, and entries that do not count (a text
// file, an index entry whose file is gone) leave that bound alone. Negative: one file more is an
// error that names the directory and what was counted, from the command too, with no report.
func TestBaselineWorktreeFilesBound(t *testing.T) {
	dir, env := baselineFixture(t)
	many := filepath.Join(dir, "many")
	writeMarkdownFiles(t, dir, "many", maxCavemanFiles)
	writeFixtureFile(t, dir, "many/notes.txt", "never an input\n")
	writeFixtureFile(t, dir, "many/staged.md", "index only\n")
	if out, err := runFixtureGit(t, dir, env, "add", "many/staged.md"); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	removeBaselinePath(t, dir, "many/staged.md")

	files, err := baselineWorktreeFiles(t.Context(), many, nil)
	if err != nil || len(files) != maxCavemanFiles {
		t.Fatalf("exactly the bound must pass: %d files, err=%v", len(files), err)
	}
	for _, file := range files {
		if !file.worktree || file.ignored || file.object != "" || file.gitDir != many {
			t.Fatalf("a listed file is a working-tree row without a base side: %+v", file)
		}
	}

	writeFixtureFile(t, dir, "many/one-more.md", "one\n")
	if _, err = baselineWorktreeFiles(t.Context(), many, nil); err == nil || !strings.Contains(err.Error(), "many "+boundMessage) {
		t.Fatalf("one file above the bound must name the directory and what was counted: %v", err)
	}
	out, err := estimateBase(t, many)
	if err == nil || out != "" || !strings.Contains(err.Error(), "many "+boundMessage+"; name its subdirectories or files") {
		t.Fatalf("want the bound error and no report: err=%v\n%.500s", err, out)
	}
	// A subdirectory of it is below the bound and is measured.
	out, err = estimateBase(t, filepath.Join(many, "00"))
	if err != nil || !strings.Contains(out, fmt.Sprintf("total: inputs=%d ", maxCavemanFiles/32)) {
		t.Fatalf("a subdirectory below the bound: err=%v\n%.500s", err, out)
	}
}

// TestBaselineWorktreeMarkdown covers which entries of git's listing are working-tree Markdown
// files. Positive: a regular file, in any letter case of the extension, at any depth.
// Negative: another extension, an index entry whose file is gone, a directory under a Markdown
// name (a submodule is listed so), the "<directory>/" entry of a nested repository and a path
// that leaves the directory. Boundary: a path below a regular file is gone on every host
// (POSIX answers ENOTDIR there), and a symlink to a Markdown file is no regular file.
func TestBaselineWorktreeMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "a.md", "one\n")
	writeFixtureFile(t, dir, "sub/deep/B.MD", "one\n")
	writeFixtureFile(t, dir, "notes.txt", "one\n")
	writeFixtureFile(t, dir, "module.md/inner.txt", "one\n")
	for rel, want := range map[string]bool{
		"a.md":           true,
		"sub/deep/B.MD":  true,
		"notes.txt":      false,
		"gone.md":        false,
		"module.md":      false,
		"sub/":           false,
		"module.md/":     false,
		"../a.md":        false,
		"/a.md":          false,
		"a.md/below.md":  false,
		"notes.txt/x.md": false,
		"":               false,
	} {
		path, counts, err := baselineWorktreeMarkdown(dir, rel)
		if err != nil || counts != want {
			t.Errorf("baselineWorktreeMarkdown(%q) = %q, %v, %v; want counts=%v", rel, path, counts, err, want)
		}
		if counts && path != filepath.Join(dir, filepath.FromSlash(rel)) {
			t.Errorf("baselineWorktreeMarkdown(%q) spelled the file %q", rel, path)
		}
	}
	symlinkOrSkip(t, "a.md", filepath.Join(dir, "link.md"))
	if path, counts, err := baselineWorktreeMarkdown(dir, "link.md"); err != nil || counts {
		t.Errorf("a symlink is no regular file: %q, %v, %v", path, counts, err)
	}
}

// TestCavemanEstimateBaseListedEntryWithoutFile runs the entries git lists that are no file
// through the command. Positive: a file staged and then removed is on neither side and is no
// row, while a tracked file removed from disk stays the worktree=absent row it was. Negative:
// a tracked file replaced by a symlink is refused by the reader, never counted as deleted.
func TestCavemanEstimateBaseListedEntryWithoutFile(t *testing.T) {
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/staged.md", "index only\n")
	if out, err := runFixtureGit(t, dir, env, "add", "docs/staged.md"); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	removeBaselinePath(t, dir, "docs/staged.md")
	removeBaselinePath(t, dir, "docs/gone.md")
	out, err := estimateBase(t, filepath.Join(dir, "docs"))
	if err != nil || strings.Contains(out, "staged.md") || !strings.Contains(out, "docs/gone.md: bytes=30->0 (-30)") ||
		!strings.Contains(out, "total: inputs=4 ") || !strings.Contains(out, "base_absent=0 worktree_absent=1 worktree_ignored=0\n") {
		t.Fatalf("an index entry without a file is no row, a deleted tracked file is one: err=%v\n%s", err, out)
	}

	removeBaselinePath(t, dir, "docs/kept.md")
	symlinkOrSkip(t, "edited.md", filepath.Join(dir, "docs", "kept.md"))
	out, err = estimateBase(t, filepath.Join(dir, "docs"))
	if err == nil || out != "" || !strings.Contains(err.Error(), "docs/kept.md") {
		t.Fatalf("a tracked file replaced by a symlink must be refused by the reader: err=%v\n%s", err, out)
	}
}
