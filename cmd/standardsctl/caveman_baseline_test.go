package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// baselineFixture is a committed repository with one Markdown file per case of a rewrite: kept,
// edited, deleted, deleted with its directory, and a text file the expansion leaves out.
// initGitFixture skips the test when git is unavailable.
func baselineFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "root.md", "top level file\n")
	writeFixtureFile(t, dir, "docs/kept.md", "one two three\n")
	writeFixtureFile(t, dir, "docs/edited.md", "alpha beta gamma delta\n")
	writeFixtureFile(t, dir, "docs/gone.md", "old text here now\nsecond line\n")
	writeFixtureFile(t, dir, "docs/old/x.md", "x y\n")
	writeFixtureFile(t, dir, "docs/notes.txt", "never an input\n")
	env := initGitFixture(t, dir)
	return dir, env
}

// outsideRepository returns a directory that is in no repository wherever the test runs: git
// stops looking for one at the directory above it (GIT_CEILING_DIRECTORIES, as in
// internal/util/git_unset_test.go), so a temporary directory below a checkout, whose branch
// main would answer --base=main, still reads as "not a git repository".
func outsideRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	return dir
}

func removeBaselinePath(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

// TestCavemanEstimateBasePositive pins #373: one command measures a rewrite against a git
// revision, per file and in total, and a file that exists on one side only is a row that says
// so. The deleted directory is found through the base listing of its parent.
func TestCavemanEstimateBasePositive(t *testing.T) {
	dir, _ := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/edited.md", "alpha beta\n")
	writeFixtureFile(t, dir, "docs/new.md", "brand new\n")
	removeBaselinePath(t, dir, "docs/gone.md")
	removeBaselinePath(t, dir, "docs/old")
	docs := filepath.Join(dir, "docs")

	out, err := runCavemanCLI(t, "", "estimate", "--base=main", docs)
	if err != nil {
		t.Fatalf("estimate --base: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`(?m)^base: main = [0-9a-f]{40}$`).MatchString(out) {
		t.Errorf("the report must open with the resolved commit:\n%s", out)
	}
	rows := []string{
		"docs/edited.md: bytes=23->11 (-12) lines=1->1 (+0) tokens_est=5->2 (-3)\n",
		"docs/gone.md: bytes=30->0 (-30) lines=2->0 (-2) tokens_est=7->0 (-7) worktree=absent\n",
		"docs/kept.md: bytes=14->14 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\n",
		"docs/new.md: bytes=0->10 (+10) lines=0->1 (+1) tokens_est=0->2 (+2) base=absent\n",
		"docs/old/x.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n",
		"total: inputs=5 bytes=71->35 (-36) tokens_est=17->7 (-10) base_absent=1 worktree_absent=2 worktree_ignored=0\n",
	}
	last := -1
	for _, row := range rows {
		at := strings.Index(out, row)
		if at <= last {
			t.Fatalf("row %q missing or out of lexical order:\n%s", row, out)
		}
		last = at
	}
	if strings.Contains(out, "notes.txt") || strings.Contains(out, "root.md") {
		t.Errorf("only the Markdown files below the directory are inputs:\n%s", out)
	}

	// A named file, with the flag after it and the value as a separate argument.
	root := filepath.Join(dir, "root.md")
	out, err = runCavemanCLI(t, "", "estimate", root, "--base", "HEAD")
	if err != nil || !strings.Contains(out, "root.md: bytes=15->15 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\ntotal: inputs=1 bytes=15->15 (+0) tokens_est=3->3 (+0) base_absent=0 worktree_absent=0 worktree_ignored=0\n") {
		t.Fatalf("named file: err=%v\n%s", err, out)
	}
	// A deleted file can be named: the base side is all there is to measure.
	out, err = runCavemanCLI(t, "", "estimate", "--base=main", filepath.Join(docs, "gone.md"), root)
	if err != nil || !strings.Contains(out, "docs/gone.md: bytes=30->0 (-30)") || !strings.Contains(out, "total: inputs=2 ") {
		t.Fatalf("named deleted file beside a kept one: err=%v\n%s", err, out)
	}
}

// TestCavemanEstimateBaseFollowsTheRevision: the before side is the named revision, not HEAD.
func TestCavemanEstimateBaseFollowsTheRevision(t *testing.T) {
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/edited.md", "alpha beta\n")
	gitCommitAll(t, dir, env, "shorten")
	if out, err := runFixtureGit(t, dir, env, "tag", "v1", "HEAD~1"); err != nil {
		t.Fatalf("tag: %v (%s)", err, out)
	}
	edited := filepath.Join(dir, "docs", "edited.md")
	for rev, want := range map[string]string{
		"HEAD":   "docs/edited.md: bytes=11->11 (+0)",
		"HEAD~1": "docs/edited.md: bytes=23->11 (-12)",
		"v1":     "docs/edited.md: bytes=23->11 (-12)",
	} {
		out, err := runCavemanCLI(t, "", "estimate", "--base="+rev, edited)
		if err != nil || !strings.Contains(out, want) || !strings.Contains(out, "base: "+rev+" = ") {
			t.Errorf("--base=%s: err=%v\n%s", rev, err, out)
		}
	}
}

func TestCavemanEstimateBaseNegative(t *testing.T) {
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "empty/notes.txt", "no markdown here\n")
	writeFixtureFile(t, dir, "docs/binary.md", "\xff\xfe not text\n")
	gitCommitAll(t, dir, env, "binary")
	writeFixtureFile(t, dir, "docs/binary.md", "text now\n")
	kept := filepath.Join(dir, "docs", "kept.md")
	outside := writeFixtureFile(t, outsideRepository(t), "plain.md", "no repository here\n")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no path":             {[]string{"estimate", "--base=main"}, "usage: praetorctl caveman"},
		"no value":            {[]string{"estimate", kept, "--base"}, "flag needs an argument"},
		"empty revision":      {[]string{"estimate", "--base=", kept}, `--base "" names no commit`},
		"unknown revision":    {[]string{"estimate", "--base=no-such-branch", kept}, `--base "no-such-branch" names no commit`},
		"option as revision":  {[]string{"estimate", "--base=--output=x", kept}, "names no commit"},
		"reflog syntax":       {[]string{"estimate", "--base=HEAD@{1}", kept}, `--base "HEAD@{1}" names no commit`},
		"stdin":               {[]string{"estimate", "--base=main", "-"}, "standard input has none"},
		"stdin beside a file": {[]string{"estimate", "--base=main", kept, "-"}, "standard input has none"},
		"on neither side":     {[]string{"estimate", "--base=main", filepath.Join(dir, "docs", "typo.md")}, "docs/typo.md is a file neither in the working tree nor at"},
		"no markdown":         {[]string{"estimate", "--base=main", filepath.Join(dir, "empty")}, "empty holds no Markdown file that git tracks or would track in the working tree, and none at"},
		"missing directory":   {[]string{"estimate", "--base=main", filepath.Join(dir, "absent", "x.md")}, "absent/x.md is a file neither in the working tree nor at"},
		"not a repository":    {[]string{"estimate", "--base=main", outside}, "caveman estimate: --base: failed to resolve ref \"main\""},
		"base is not text":    {[]string{"estimate", "--base=HEAD", filepath.Join(dir, "docs", "binary.md")}, "docs/binary.md at the base revision is not UTF-8 text"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
				t.Fatalf("want an error holding %q and no report: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

// deletedDirFixture commits a directory tree, a directory without Markdown and a directory of
// one file on top of baselineFixture, then removes all of them from the working tree, so every
// path below them exists at HEAD only.
func deletedDirFixture(t *testing.T) string {
	t.Helper()
	dir, env := baselineFixture(t)
	writeFixtureFile(t, dir, "docs/old/deep/y.md", "one two three\n")
	writeFixtureFile(t, dir, "docs/old/n.txt", "plain text\n")
	writeFixtureFile(t, dir, "docs/single/only.md", "one\n")
	writeFixtureFile(t, dir, "assets/logo.txt", "no markdown\n")
	writeFixtureFile(t, dir, "gone/a/b/c.md", "one two\n")
	gitCommitAll(t, dir, env, "directories")
	for _, rel := range []string{"docs/old", "docs/single", "assets", "gone"} {
		removeBaselinePath(t, dir, rel)
	}
	return dir
}

// TestCavemanEstimateBaseDeletedDirectory pins the review finding on #373: a path whose
// directory the working tree no longer holds is read from the base revision like any other
// deleted file. Git used to be started in that missing directory, so naming such a file was a
// "fork/exec git: no such file or directory" error instead of a worktree=absent row.
func TestCavemanEstimateBaseDeletedDirectory(t *testing.T) {
	dir := deletedDirFixture(t)
	x := filepath.Join(dir, "docs", "old", "x.md")
	xRow := "docs/old/x.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n"
	yRow := "docs/old/deep/y.md: bytes=14->0 (-14) lines=1->0 (-1) tokens_est=3->0 (-3) worktree=absent\n"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		// Named first, the revision is resolved in the nearest directory that still exists.
		"file named first": {[]string{x}, xRow + "total: inputs=1 bytes=4->0 (-4) tokens_est=2->0 (-2) base_absent=0 worktree_absent=1 worktree_ignored=0\n"},
		"file named after a kept one": {
			[]string{filepath.Join(dir, "root.md"), x},
			"root.md: bytes=15->15 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\n" + filepath.ToSlash(dir) + "/" + xRow +
				"total: inputs=2 bytes=19->15 (-4) tokens_est=5->3 (-2) base_absent=0 worktree_absent=1 worktree_ignored=0\n",
		},
		// A named file is measured whatever its extension, as it is in the working tree.
		"text file": {[]string{filepath.Join(dir, "docs", "old", "n.txt")}, "docs/old/n.txt: bytes=11->0 (-11) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n"},
		// A deleted directory is its Markdown files at the base, in lexical order.
		"directory": {
			[]string{filepath.Join(dir, "docs", "old")},
			yRow + filepath.ToSlash(dir) + "/" + xRow + "total: inputs=2 bytes=18->0 (-18) tokens_est=5->0 (-5) base_absent=0 worktree_absent=2 worktree_ignored=0\n",
		},
		"several levels gone": {[]string{filepath.Join(dir, "gone", "a", "b", "c.md")}, "gone/a/b/c.md: bytes=8->0 (-8) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n"},
		"top directory gone":  {[]string{filepath.Join(dir, "gone")}, "gone/a/b/c.md: bytes=8->0 (-8) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\ntotal: inputs=1 "},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", append([]string{"estimate", "--base=HEAD"}, tc.args...)...)
			if err != nil || !strings.Contains(out, tc.want) || strings.Contains(out, "n.txt") != (name == "text file") {
				t.Fatalf("want a report holding %q: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

// TestCavemanEstimateBaseDeletedDirectoryNegative: a path below a missing directory that the
// base does not hold either stays an error that names the path, never a git start failure.
func TestCavemanEstimateBaseDeletedDirectoryNegative(t *testing.T) {
	dir := deletedDirFixture(t)
	outside := filepath.Join(outsideRepository(t), "gone", "plain.md")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"typo in a deleted directory":    {[]string{filepath.Join(dir, "docs", "old", "typo.md")}, "docs/old/typo.md is a file neither in the working tree nor at"},
		"typo after a kept file":         {[]string{filepath.Join(dir, "root.md"), filepath.Join(dir, "docs", "old", "typo.md")}, "docs/old/typo.md is a file neither in the working tree nor at"},
		"directory that never existed":   {[]string{filepath.Join(dir, "absent", "x.md")}, "absent/x.md is a file neither in the working tree nor at"},
		"path through a file":            {[]string{filepath.Join(dir, "docs", "kept.md", "x.md")}, "docs/kept.md/x.md is a file neither in the working tree nor at"},
		"deleted directory, no text":     {[]string{filepath.Join(dir, "assets")}, "assets holds no Markdown file in the working tree or at"},
		"deleted path, not a repository": {[]string{outside}, "caveman estimate: --base: failed to resolve ref \"HEAD\""},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", append([]string{"estimate", "--base=HEAD"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "fork/exec") || out != "" {
				t.Fatalf("want an error holding %q, no git start failure and no report: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

// TestCavemanEstimateBaseDeletedDirectoryBoundary covers the edges of the file-or-directory
// answer the base listing gives for a path the working tree lacks.
func TestCavemanEstimateBaseDeletedDirectoryBoundary(t *testing.T) {
	dir := deletedDirFixture(t)
	// A deleted directory of exactly one file is one listing entry, as a deleted file is; the
	// row is the file below the directory, not the directory.
	out, err := runCavemanCLI(t, "", "estimate", "--base=HEAD", filepath.Join(dir, "docs", "single"))
	if err != nil || !strings.Contains(out, "docs/single/only.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=1->0 (-1) worktree=absent\ntotal: inputs=1 ") {
		t.Fatalf("directory of one file: err=%v\n%s", err, out)
	}
	// A directory replaced by a file of its name: the files it held are absent from the working
	// tree on every host (POSIX answers ENOTDIR, Windows a missing path).
	writeFixtureFile(t, dir, "docs/old", "a file now\n")
	out, err = runCavemanCLI(t, "", "estimate", "--base=HEAD", filepath.Join(dir, "docs", "old", "x.md"))
	if err != nil || !strings.Contains(out, "docs/old/x.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n") {
		t.Fatalf("directory replaced by a file: err=%v\n%s", err, out)
	}
	// Relative spellings, the form `git diff --name-only | xargs` hands over: git starts in the
	// nearest existing directory, here the repository root itself (".").
	t.Chdir(dir)
	out, err = runCavemanCLI(t, "", "estimate", "--base=HEAD", "gone/a/b/c.md", "root.md", "docs/single")
	want := "gone/a/b/c.md: bytes=8->0 (-8) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent\n" +
		"root.md: bytes=15->15 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\n" +
		"docs/single/only.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=1->0 (-1) worktree=absent\n" +
		"total: inputs=3 bytes=27->15 (-12) tokens_est=6->3 (-3) base_absent=0 worktree_absent=2 worktree_ignored=0\n"
	if err != nil || !strings.HasSuffix(out, want) {
		t.Fatalf("relative paths: err=%v\n%s\nwant suffix:\n%s", err, out, want)
	}
}

func TestCavemanEstimateBaseBoundary(t *testing.T) {
	dir, env := baselineFixture(t)
	// A directory the base does not hold: every file is new, none is skipped.
	writeFixtureFile(t, dir, "fresh/a.md", "one\n")
	out, err := runCavemanCLI(t, "", "estimate", "--base=main", filepath.Join(dir, "fresh"))
	if err != nil || !strings.Contains(out, "fresh/a.md: bytes=0->4 (+4) lines=0->1 (+1) tokens_est=0->1 (+1) base=absent\ntotal: inputs=1 bytes=0->4 (+4) tokens_est=0->1 (+1) base_absent=1 worktree_absent=0 worktree_ignored=0\n") {
		t.Fatalf("new directory: err=%v\n%s", err, out)
	}
	// A name git would read as a glob is looked up literally, and an empty file on both sides
	// is a row of zeros rather than an absent side.
	writeFixtureFile(t, dir, "docs/[x].md", "bracket\n")
	writeFixtureFile(t, dir, "docs/empty.md", "")
	// A blob of exactly the reader's bound is measured; one byte more is refused.
	writeFixtureFile(t, dir, "docs/limit.md", strings.Repeat("a", 1<<20))
	writeFixtureFile(t, dir, "docs/over.md", strings.Repeat("a", 1<<20+1))
	gitCommitAll(t, dir, env, "boundary")
	writeFixtureFile(t, dir, "docs/limit.md", "small\n")
	writeFixtureFile(t, dir, "docs/over.md", "small\n")
	for name, want := range map[string]string{
		"[x].md":   "docs/[x].md: bytes=8->8 (+0) lines=1->1 (+0) tokens_est=1->1 (+0)\n",
		"empty.md": "docs/empty.md: bytes=0->0 (+0) lines=0->0 (+0) tokens_est=0->0 (+0)\n",
		"limit.md": "docs/limit.md: bytes=1048576->6 (-1048570) lines=1->1 (+0) tokens_est=1->1 (+0)\n",
	} {
		out, err = runCavemanCLI(t, "", "estimate", "--base=HEAD", filepath.Join(dir, "docs", name))
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("%s: err=%v\n%s", name, err, out)
		}
	}
	out, err = runCavemanCLI(t, "", "estimate", "--base=HEAD", filepath.Join(dir, "docs", "over.md"))
	if err == nil || !strings.Contains(err.Error(), "docs/over.md at the base revision exceeds 1048576 bytes") || out != "" {
		t.Fatalf("a base blob above the bound must be refused: err=%v\n%s", err, out)
	}
	// Without --base the command is the plain estimate it was.
	out, err = runCavemanCLI(t, "", "estimate", filepath.Join(dir, "docs", "kept.md"))
	if err != nil || !strings.Contains(out, "docs/kept.md: bytes=14 lines=1 tokens_est=3\ntotal: inputs=1 bytes=14 tokens_est=3\n") {
		t.Fatalf("plain estimate changed: err=%v\n%s", err, out)
	}
}

// TestCavemanDeltaRow pins the row both compress and estimate --base print: the keys of the
// plain estimate line, each as before, after and signed difference.
func TestCavemanDeltaRow(t *testing.T) {
	before := measureCavemanText("one two three\nfour\n")
	after := measureCavemanText("one\n")
	if before != (cavemanMeasure{bytes: 19, lines: 2, tokens: 5}) || after != (cavemanMeasure{bytes: 4, lines: 1, tokens: 1}) {
		t.Fatalf("measure: %+v %+v", before, after)
	}
	for name, tc := range map[string]struct {
		delta cavemanDelta
		want  string
	}{
		"shrink": {cavemanDelta{name: "a.md", before: before, after: after}, "a.md: bytes=19->4 (-15) lines=2->1 (-1) tokens_est=5->1 (-4)"},
		"grow":   {cavemanDelta{name: "a.md", before: after, after: before}, "a.md: bytes=4->19 (+15) lines=1->2 (+1) tokens_est=1->5 (+4)"},
		"same":   {cavemanDelta{name: "-", before: after, after: after}, "-: bytes=4->4 (+0) lines=1->1 (+0) tokens_est=1->1 (+0)"},
		"empty":  {cavemanDelta{name: "e.md"}, "e.md: bytes=0->0 (+0) lines=0->0 (+0) tokens_est=0->0 (+0)"},
	} {
		if got := tc.delta.row(); got != tc.want {
			t.Errorf("%s: row = %q, want %q", name, got, tc.want)
		}
	}
	if got := cavemanDeltaTotal(2, before.plus(after), after); got != "total: inputs=2 bytes=23->4 (-19) tokens_est=6->1 (-5)" {
		t.Errorf("total = %q", got)
	}
}
