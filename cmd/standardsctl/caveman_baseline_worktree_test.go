package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// symlinkOrSkip links name to target, and skips where the host refuses symlinks (Windows
// without the privilege to create them).
func symlinkOrSkip(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("symlinks are unavailable here: %v", err)
	}
}

// estimateBase runs `caveman estimate --base=HEAD` on paths.
func estimateBase(t *testing.T, paths ...string) (string, error) {
	t.Helper()
	return runCavemanCLI(t, "", append([]string{"estimate", "--base=HEAD"}, paths...)...)
}

// TestCavemanEstimateBaseRefusesSymlinkedDirectory pins the major review finding on #373: a
// symlink to a directory named as the directory printed every file of its target as
// worktree=absent and exited 0, because the walk does not follow the link while git lists its
// target. It is refused with the error the reader gives a file below such a link.
func TestCavemanEstimateBaseRefusesSymlinkedDirectory(t *testing.T) {
	dir, env := baselineFixture(t)
	link := filepath.Join(dir, "docs-link")
	symlinkOrSkip(t, "docs", link)
	gitCommitAll(t, dir, env, "link")
	const refusal = "confinement root must be a directory, never a symlink"

	_, readerErr := estimateBase(t, filepath.Join(link, "kept.md"))
	if readerErr == nil || !strings.Contains(readerErr.Error(), refusal) {
		t.Fatalf("the reader must refuse a file below the link: %v", readerErr)
	}
	for name, path := range map[string]string{
		"link":               link,
		"link, trailing":     link + string(filepath.Separator),
		"link, after a file": link,
	} {
		t.Run(name, func(t *testing.T) {
			paths := []string{path}
			if name == "link, after a file" {
				paths = []string{filepath.Join(dir, "root.md"), path}
			}
			out, err := estimateBase(t, paths...)
			if err == nil || out != "" || !strings.Contains(err.Error(), "caveman estimate: read ") || !strings.Contains(err.Error(), refusal) {
				t.Fatalf("want the reader's refusal and no report: err=%v\n%s", err, out)
			}
			if strings.Contains(out, "worktree=absent") {
				t.Fatalf("files of the link target were reported as deleted:\n%s", out)
			}
		})
	}
}

// TestCavemanEstimateBaseSymlinkBoundary: only the directory named is held to be no symlink.
// A path that reaches it through one is read, as the reader reads it (the temporary directory
// of macOS sits below such a link), and a tracked directory that a symlink has replaced is
// refused by the reader rather than counted as deleted files.
func TestCavemanEstimateBaseSymlinkBoundary(t *testing.T) {
	dir, _ := baselineFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	symlinkOrSkip(t, dir, alias)
	out, err := estimateBase(t, filepath.Join(alias, "docs"))
	if err != nil || !strings.Contains(out, "docs/kept.md: bytes=14->14 (+0)") || strings.Contains(out, "=absent\n") ||
		!strings.Contains(out, "total: inputs=4 ") {
		t.Fatalf("a directory reached through a symlinked ancestor: err=%v\n%s", err, out)
	}

	elsewhere := t.TempDir()
	writeFixtureFile(t, elsewhere, "x.md", "x y\n")
	removeBaselinePath(t, dir, "docs/old")
	symlinkOrSkip(t, elsewhere, filepath.Join(dir, "docs", "old"))
	out, err = estimateBase(t, filepath.Join(dir, "docs"))
	if err == nil || out != "" || !strings.Contains(err.Error(), "docs/old/x.md") {
		t.Fatalf("a tracked directory replaced by a symlink must be refused, not read as deleted: err=%v\n%s", err, out)
	}
}

// ignoreFixture is baselineFixture plus ignore rules, committed, and then the files git
// leaves alone: ignored by pattern, inside an ignored directory, and inside a nested
// repository. docs/untracked.md is new and not ignored, so git would track it.
func ignoreFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, ".gitignore", "*.scratch.md\nledger/\n")
	writeFixtureFile(t, dir, "docs/forced.scratch.md", "kept by force\n")
	if out, err := runFixtureGit(t, dir, env, "add", "-f", "docs/forced.scratch.md"); err != nil {
		t.Fatalf("git add -f: %v (%s)", err, out)
	}
	gitCommitAll(t, dir, env, "ignore rules")
	writeFixtureFile(t, dir, "docs/untracked.md", "brand new\n")
	writeFixtureFile(t, dir, "docs/notes.scratch.md", "one two three four\n")
	writeFixtureFile(t, dir, "docs/ledger/state.md", "one two\n")
	writeFixtureFile(t, dir, "ledger/open.md", "one\n")
	writeFixtureFile(t, dir, "docs/nested/n.md", "nested text\n")
	if out, err := runFixtureGit(t, filepath.Join(dir, "docs", "nested"), env, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("nested git init: %v (%s)", err, out)
	}
	return dir, env
}

// TestCavemanEstimateBaseDirectoryCountsWhatGitTracks pins the review finding on #373: the
// working-tree side of a directory is the files git tracks or would track, so it describes
// the set the base side can hold. Ignored files and the files of a nested repository used to
// be base=absent rows that inflated the total.
func TestCavemanEstimateBaseDirectoryCountsWhatGitTracks(t *testing.T) {
	dir, _ := ignoreFixture(t)
	out, err := estimateBase(t, filepath.Join(dir, "docs"))
	if err != nil {
		t.Fatalf("estimate --base: %v\n%s", err, out)
	}
	for _, left := range []string{"notes.scratch.md", "ledger/state.md", "nested/n.md"} {
		if strings.Contains(out, left) {
			t.Errorf("%s is a file git neither tracks nor would track, and must not be a row:\n%s", left, out)
		}
	}
	for _, want := range []string{
		"docs/forced.scratch.md: bytes=14->14 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\n",
		"docs/untracked.md: bytes=0->10 (+10) lines=0->1 (+1) tokens_est=0->2 (+2) base=absent\n",
		"total: inputs=6 bytes=85->95 (+10) tokens_est=20->22 (+2) base_absent=1 worktree_absent=0 worktree_ignored=0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	// The repository root is the run the finding names: the ledger directory stays out too.
	out, err = estimateBase(t, dir)
	if err != nil || strings.Contains(out, "ledger/") || strings.Contains(out, "nested/") || !strings.Contains(out, "root.md: bytes=15->15 (+0)") {
		t.Fatalf("repository root: err=%v\n%s", err, out)
	}
}

// TestCavemanEstimateBaseIgnoredNegative: a directory with nothing git tracks or would track
// is an error, as a directory without Markdown is; nothing ignored turns into a row through
// it.
func TestCavemanEstimateBaseIgnoredNegative(t *testing.T) {
	dir, _ := ignoreFixture(t)
	for name, rel := range map[string]string{
		"ignored directory":            "ledger",
		"ignored directory below docs": "docs/ledger",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := estimateBase(t, filepath.Join(dir, filepath.FromSlash(rel)))
			if err == nil || out != "" || !strings.Contains(err.Error(), rel+" holds no Markdown file that git tracks or would track in the working tree, and none at") {
				t.Fatalf("want the no-Markdown error and no report: err=%v\n%s", err, out)
			}
		})
	}
}

// TestCavemanEstimateBaseIgnoredBoundary: a file named outright is measured even when git
// ignores it, and its row says so; a file the base holds stays a row when git has stopped
// tracking it; and a tracked file an ignore rule also matches is an ordinary row.
func TestCavemanEstimateBaseIgnoredBoundary(t *testing.T) {
	dir, env := ignoreFixture(t)
	scratch := filepath.Join(dir, "docs", "notes.scratch.md")
	out, err := estimateBase(t, scratch, filepath.Join(dir, "ledger", "open.md"), filepath.Join(dir, "docs", "kept.md"))
	want := []string{
		"docs/notes.scratch.md: bytes=0->19 (+19) lines=0->1 (+1) tokens_est=0->5 (+5) base=absent worktree=ignored\n",
		"ledger/open.md: bytes=0->4 (+4) lines=0->1 (+1) tokens_est=0->1 (+1) base=absent worktree=ignored\n",
		"docs/kept.md: bytes=14->14 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\n",
		"total: inputs=3 bytes=14->37 (+23) tokens_est=3->9 (+6) base_absent=2 worktree_absent=0 worktree_ignored=2\n",
	}
	for _, row := range want {
		if err != nil || !strings.Contains(out, row) {
			t.Fatalf("named ignored files: err=%v, report lacks %q:\n%s", err, row, out)
		}
	}
	out, err = estimateBase(t, filepath.Join(dir, "docs", "forced.scratch.md"), filepath.Join(dir, "docs", "untracked.md"))
	if err != nil || strings.Contains(out, "worktree=ignored\n") || !strings.Contains(out, "worktree_ignored=0\n") {
		t.Fatalf("a tracked file and a file git would track are not ignored: err=%v\n%s", err, out)
	}

	// Tracked at the base, ignored since: the directory still reports it, with both sides.
	writeFixtureFile(t, dir, ".gitignore", "*.scratch.md\nledger/\nkept.md\n")
	if gitOut, gitErr := runFixtureGit(t, dir, env, "rm", "-q", "--cached", "docs/kept.md"); gitErr != nil {
		t.Fatalf("git rm --cached: %v (%s)", gitErr, gitOut)
	}
	out, err = estimateBase(t, filepath.Join(dir, "docs"))
	if err != nil || !strings.Contains(out, "docs/kept.md: bytes=14->14 (+0) lines=1->1 (+0) tokens_est=3->3 (+0) worktree=ignored\n") ||
		!strings.Contains(out, "worktree_absent=0 worktree_ignored=1\n") {
		t.Fatalf("a file the base holds and git now ignores: err=%v\n%s", err, out)
	}
}

// TestCaseVariants covers the pure half of the letter-case check: which tracked spellings of
// a listing are the named path in another case.
func TestCaseVariants(t *testing.T) {
	listing := []string{"docs/README.md", "docs/readme.md", "docs/Readme.md", "Docs/guide/a.md", "docs/guide/b.md", "DOCS/guide/c.md", "README.md"}
	for name, tc := range map[string]struct {
		named string
		want  []string
	}{
		// Positive: a file and a directory, each under every other spelling the listing holds.
		"file":      {"docs/readme.md", []string{"docs/README.md", "docs/Readme.md"}},
		"directory": {"docs/Guide", []string{"Docs/guide", "docs/guide", "DOCS/guide"}},
		"top level": {"readme.md", []string{"README.md"}},
		// Negative: the exact spelling is no variant, and neither is another name.
		"exact only":     {"README.md", nil},
		"another name":   {"docs/other.md", nil},
		"prefix of name": {"docs/READ", nil},
		// Boundary: an entry with fewer segments than the name cannot spell it, and a directory
		// is listed once however many files it holds.
		"deeper than the listing": {"docs/readme.md/x", nil},
		"one directory, once":     {"DOCS", []string{"docs", "Docs"}},
	} {
		if got := caseVariants(tc.named, listing); !slices.Equal(got, tc.want) {
			t.Errorf("%s: caseVariants(%q) = %v, want %v", name, tc.named, got, tc.want)
		}
	}
	many := make([]string, 0, maxBaselineSpellings+4)
	for i := range maxBaselineSpellings + 4 {
		many = append(many, strings.Repeat("A", i+1)+strings.Repeat("a", maxBaselineSpellings+4-i)+".md")
	}
	if got := caseVariants(strings.ToLower(many[0]), many); len(got) != maxBaselineSpellings {
		t.Errorf("the spellings compared must stay bounded: %d", len(got))
	}
}

// trackedSpellingFixture commits docs/README.md and returns the repository with a second
// spelling of that one file, docs/readme.md. Where the file system ignores case the spelling
// is there already; elsewhere a hard link provides it, which is the same situation for the
// command: two spellings, one file, one of them tracked.
func trackedSpellingFixture(t *testing.T) (string, bool) {
	t.Helper()
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/README.md", "read me first\n")
	gitCommitAll(t, dir, env, "readme")
	writeFixtureFile(t, dir, "docs/README.md", "read me\n")
	insensitive := caseInsensitiveDir(t, dir)
	if !insensitive {
		if err := os.Link(filepath.Join(dir, "docs", "README.md"), filepath.Join(dir, "docs", "readme.md")); err != nil {
			t.Skipf("neither a case-insensitive file system nor hard links here: %v", err)
		}
	}
	return dir, insensitive
}

// TestCavemanEstimateBaseTrackedSpelling pins the review finding on #373: a file named in a
// letter case git does not track it under passed the working-tree lookup on a file system
// that ignores case and printed base=absent, before=0, exit 0. It is an error naming the
// tracked spelling.
func TestCavemanEstimateBaseTrackedSpelling(t *testing.T) {
	dir, _ := trackedSpellingFixture(t)
	out, err := estimateBase(t, filepath.Join(dir, "docs", "readme.md"))
	if err == nil || out != "" || !strings.Contains(err.Error(), "docs/readme.md is tracked as docs/README.md; name it in that letter case") {
		t.Fatalf("want an error naming the tracked spelling and no report: err=%v\n%s", err, out)
	}
	// Named as tracked it is measured, both sides.
	out, err = estimateBase(t, filepath.Join(dir, "docs", "README.md"))
	if err != nil || !strings.Contains(out, "docs/README.md: bytes=14->8 (-6) lines=1->1 (+0) tokens_est=3->2 (-1)\n") {
		t.Fatalf("the tracked spelling: err=%v\n%s", err, out)
	}
	// From inside the directory, by a relative name: the spelling is given from the top.
	t.Chdir(filepath.Join(dir, "docs"))
	if _, err = estimateBase(t, "readme.md"); err == nil || !strings.Contains(err.Error(), "readme.md is tracked as docs/README.md") {
		t.Fatalf("relative name: %v", err)
	}
}

// TestCavemanEstimateBaseTrackedSpellingNegative: where the file system distinguishes case,
// a file whose name differs from a tracked one in case only is its own file, and a new one.
func TestCavemanEstimateBaseTrackedSpellingNegative(t *testing.T) {
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/README.md", "read me first\n")
	gitCommitAll(t, dir, env, "readme")
	if caseInsensitiveDir(t, dir) {
		t.Skip("this file system ignores letter case: readme.md and README.md cannot both exist here")
	}
	writeFixtureFile(t, dir, "docs/readme.md", "another file\n")
	writeFixtureFile(t, dir, "Docs/a.md", "one\n")
	out, err := estimateBase(t, filepath.Join(dir, "docs", "readme.md"), filepath.Join(dir, "Docs"))
	if err != nil || !strings.Contains(out, "docs/readme.md: bytes=0->13 (+13) lines=0->1 (+1) tokens_est=0->2 (+2) base=absent\n") ||
		!strings.Contains(out, "Docs/a.md: bytes=0->4 (+4) lines=0->1 (+1) tokens_est=0->1 (+1) base=absent\n") {
		t.Fatalf("two files that differ in case are two files here: err=%v\n%s", err, out)
	}
}

// TestCavemanEstimateBaseCaseInsensitiveFileSystem runs where the file system ignores letter
// case (the default on macOS and Windows) and is skipped elsewhere: no spelling of a tracked
// path may produce a base=absent row there. A file is an error naming the tracked spelling,
// under a wrong-case parent directory too; a directory is that error or, where git itself
// resolves the directory's spelling, the true rows.
func TestCavemanEstimateBaseCaseInsensitiveFileSystem(t *testing.T) {
	dir, insensitive := trackedSpellingFixture(t)
	if !insensitive {
		t.Skip("this file system distinguishes letter case; TestCavemanEstimateBaseTrackedSpelling covers the check through a hard link")
	}
	for name, path := range map[string]string{
		"file":                   filepath.Join(dir, "docs", "Readme.MD"),
		"parent directory":       filepath.Join(dir, "DOCS", "README.md"),
		"parent and file":        filepath.Join(dir, "Docs", "readme.md"),
		"directory":              filepath.Join(dir, "DOCS"),
		"directory, mixed case":  filepath.Join(dir, "dOcS"),
		"file in a subdirectory": filepath.Join(dir, "docs", "OLD", "x.md"),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := estimateBase(t, path)
			if strings.Contains(out, "base=absent") {
				t.Fatalf("a tracked path read as absent at the base:\n%s", out)
			}
			if err != nil && !strings.Contains(err.Error(), " is tracked as docs") {
				t.Fatalf("want the tracked spelling named: %v", err)
			}
			if err == nil && !strings.Contains(name, "directory") {
				t.Fatalf("a file named in another letter case must be an error:\n%s", out)
			}
		})
	}
}
