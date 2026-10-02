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
		"total: inputs=5 bytes=71->35 (-36) tokens_est=17->7 (-10) base_absent=1 worktree_absent=2\n",
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
	if err != nil || !strings.Contains(out, "root.md: bytes=15->15 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)\ntotal: inputs=1 bytes=15->15 (+0) tokens_est=3->3 (+0) base_absent=0 worktree_absent=0\n") {
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
	outside := writeFixtureFile(t, t.TempDir(), "plain.md", "no repository here\n")
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
		"no markdown":         {[]string{"estimate", "--base=main", filepath.Join(dir, "empty")}, "holds no Markdown file in the working tree or at"},
		"missing directory":   {[]string{"estimate", "--base=main", filepath.Join(dir, "absent", "x.md")}, "caveman estimate:"},
		"not a repository":    {[]string{"estimate", "--base=main", outside}, "caveman estimate: --base"},
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

func TestCavemanEstimateBaseBoundary(t *testing.T) {
	dir, env := baselineFixture(t)
	// A directory the base does not hold: every file is new, none is skipped.
	writeFixtureFile(t, dir, "fresh/a.md", "one\n")
	out, err := runCavemanCLI(t, "", "estimate", "--base=main", filepath.Join(dir, "fresh"))
	if err != nil || !strings.Contains(out, "fresh/a.md: bytes=0->4 (+4) lines=0->1 (+1) tokens_est=0->1 (+1) base=absent\ntotal: inputs=1 bytes=0->4 (+4) tokens_est=0->1 (+1) base_absent=1 worktree_absent=0\n") {
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
