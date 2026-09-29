package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// statusRepo initialises a work tree holding one committed file at a.txt and one at
// sub/b.txt, or skips the test when git is unavailable (HISS-21: the helper asks git, and a
// host without git cannot answer).
// Note: package util cannot import internal/testsupport because testsupport depends on
// util (import cycle not allowed); fixture commits are configured directly here.
func statusRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	t.Setenv("GIT_MAINTENANCE_AUTO", "0")
	dir := t.TempDir()
	writeStatusFile(t, dir, "a.txt", "a\n")
	writeStatusFile(t, dir, filepath.Join("sub", "b.txt"), "b\n")
	statusGit(t, dir, "init", "-q")
	statusGit(t, dir, "add", "-A")
	statusGit(t, dir, "-c", "user.name=praetor-test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture")
	return dir
}

func writeStatusFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func statusGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := RunGit(t.Context(), dir, args...); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func mustChanges(t *testing.T, dir string, pathspec ...string) []string {
	t.Helper()
	changes, err := GitWorkingTreeChanges(t.Context(), dir, GitTreeProbeTimeout, pathspec...)
	if err != nil {
		t.Fatalf("GitWorkingTreeChanges: %v", err)
	}
	return changes
}

// TestGitWorkingTreeChanges_Positive_ReportsEveryKindOfChange: a modified, a staged and an
// untracked file inside an untracked directory are each reported, the last by its own path
// rather than collapsed into the directory.
func TestGitWorkingTreeChanges_Positive_ReportsEveryKindOfChange(t *testing.T) {
	dir := statusRepo(t)
	if got := mustChanges(t, dir); len(got) != 0 {
		t.Fatalf("a freshly committed tree reported changes: %q", got)
	}
	writeStatusFile(t, dir, "a.txt", "edited\n")
	writeStatusFile(t, dir, "staged.txt", "s\n")
	statusGit(t, dir, "add", "staged.txt")
	writeStatusFile(t, dir, filepath.Join("scratch", "deep", "note.txt"), "n\n")

	got := mustChanges(t, dir)
	want := []string{" M a.txt", "A  staged.txt", "?? scratch/deep/note.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
}

// TestGitWorkingTreeChanges_Negative_RepositorySettingsCannotHideChanges: the settings a
// repository can use to make a dirty tree look clean are overridden or refused.
func TestGitWorkingTreeChanges_Negative_RepositorySettingsCannotHideChanges(t *testing.T) {
	t.Run("showUntrackedFiles=no", func(t *testing.T) {
		dir := statusRepo(t)
		statusGit(t, dir, "config", "status.showUntrackedFiles", "no")
		writeStatusFile(t, dir, "hidden.txt", "h\n")
		if got := mustChanges(t, dir); !slices.Equal(got, []string{"?? hidden.txt"}) {
			t.Fatalf("status.showUntrackedFiles=no hid an untracked file: %q", got)
		}
	})
	t.Run("assume-unchanged and skip-worktree", func(t *testing.T) {
		dir := statusRepo(t)
		statusGit(t, dir, "update-index", "--assume-unchanged", "a.txt")
		statusGit(t, dir, "update-index", "--skip-worktree", "sub/b.txt")
		writeStatusFile(t, dir, "a.txt", "edited behind the flag\n")
		got := mustChanges(t, dir)
		want := []string{"assume-unchanged a.txt", "skip-worktree sub/b.txt"}
		if !slices.Equal(got, want) {
			t.Fatalf("hidden index entries = %q, want %q", got, want)
		}
	})
	t.Run("clean filter", func(t *testing.T) {
		dir := statusRepo(t)
		statusGit(t, dir, "config", "filter.normalise.clean", "cat")
		writeStatusFile(t, dir, ".gitattributes", "a.txt filter=normalise\n")
		_, err := GitWorkingTreeChanges(t.Context(), dir, GitTreeProbeTimeout)
		if !errors.Is(err, ErrGitStatusFilters) || !strings.Contains(err.Error(), "filter.normalise.clean") {
			t.Fatalf("a configured clean filter was not refused: %v", err)
		}
	})
	t.Run("not a repository", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
		if _, err := GitWorkingTreeChanges(t.Context(), t.TempDir(), GitTreeProbeTimeout); err == nil {
			t.Fatal("a directory outside any work tree answered as clean")
		}
	})
}

// TestGitWorkingTreeChanges_Boundary_ScopeAndRenames: a subdirectory still sees the whole
// repository, an exclude pathspec is resolved against dir, and a rename is one record.
func TestGitWorkingTreeChanges_Boundary_ScopeAndRenames(t *testing.T) {
	dir := statusRepo(t)
	writeStatusFile(t, dir, "top.txt", "t\n")
	sub := filepath.Join(dir, "sub")
	if got := mustChanges(t, sub); !slices.Equal(got, []string{"?? top.txt"}) {
		t.Fatalf("a subdirectory probe must cover the whole repository: %q", got)
	}

	writeStatusFile(t, dir, "receipt.json", "r\n")
	writeStatusFile(t, dir, filepath.Join("sub", "receipt.json"), "r\n")
	got := mustChanges(t, dir, ":(exclude)receipt.json")
	if !slices.Equal(got, []string{"?? sub/receipt.json", "?? top.txt"}) {
		t.Fatalf("the exclude pathspec must drop only dir's own receipt.json: %q", got)
	}
	everything := []string{":(exclude)receipt.json", ":(exclude)sub/receipt.json", ":(exclude)top.txt"}
	if got := mustChanges(t, dir, everything...); len(got) != 0 {
		t.Fatalf("every change excluded, yet reported: %q", got)
	}

	statusGit(t, dir, "mv", "a.txt", "renamed.txt")
	got = mustChanges(t, dir, everything...)
	if !slices.Equal(got, []string{"R  renamed.txt <- a.txt"}) {
		t.Fatalf("a rename must be one record naming both paths: %q", got)
	}
}

func TestHiddenIndexEntries_Boundary_MalformedRecordsAreSkipped(t *testing.T) {
	out := []byte("H tracked\x00h assumed\x00S sparse\x00s both\x00M conflicted\x00noseparator\x00HH odd tag\x00")
	got := hiddenIndexEntries(out)
	want := []string{"assume-unchanged assumed", "skip-worktree sparse", "skip-worktree both"}
	if !slices.Equal(got, want) {
		t.Fatalf("hidden = %q, want %q", got, want)
	}
	if got := hiddenIndexEntries(nil); len(got) != 0 {
		t.Fatalf("empty listing reported hidden entries: %q", got)
	}
}

// TestGitWorkingTreeChanges_Boundary_CallerBound: the caller's bound governs the whole probe. A
// non-positive bound is refused rather than read as unbounded, and a bound too short to finish
// is an error, never a clean answer.
func TestGitWorkingTreeChanges_Boundary_CallerBound(t *testing.T) {
	dir := statusRepo(t)
	for _, bound := range []time.Duration{0, -time.Second} {
		if _, err := GitWorkingTreeChanges(t.Context(), dir, bound); err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Fatalf("bound %v: want a refusal, got %v", bound, err)
		}
		if _, err := RunGitProbeWithin(t.Context(), dir, MaxCommandOutputBytes, bound, "status"); err == nil {
			t.Fatalf("RunGitProbeWithin bound %v: want a refusal", bound)
		}
	}
	if changes, err := GitWorkingTreeChanges(t.Context(), dir, time.Nanosecond); err == nil {
		t.Fatalf("a probe that cannot finish within its bound answered %q", changes)
	}
	if changes, err := GitWorkingTreeChanges(t.Context(), dir, GitTreeProbeTimeout); err != nil || len(changes) != 0 {
		t.Fatalf("a clean fixture within the default bound: changes %q, err %v", changes, err)
	}
}

// TestRefuseGitStatusFilters_3D: no filter passes, a clean or process filter a tracked path
// selects is refused, and a smudge-only filter -- which a status probe never runs -- passes; a
// probe that cannot run is an error, never an all-clear.
func TestRefuseGitStatusFilters_3D(t *testing.T) {
	dir := statusRepo(t)
	if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
		t.Fatalf("no filters configured: %v", err)
	}
	statusGit(t, dir, "config", "filter.lfs.smudge", "cat")
	writeStatusFile(t, dir, ".gitattributes", "*.txt filter=lfs\n")
	if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
		t.Fatalf("a smudge-only filter must pass: %v", err)
	}
	for _, key := range []string{"filter.lfs.clean", "filter.lfs.process"} {
		probe := statusRepo(t)
		statusGit(t, probe, "config", key, "cat")
		writeStatusFile(t, probe, ".gitattributes", "*.txt filter=lfs\n")
		if err := RefuseGitStatusFilters(t.Context(), probe); !errors.Is(err, ErrGitStatusFilters) || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s must be refused by name, got %v", key, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RefuseGitStatusFilters(cancelled, dir); err == nil || errors.Is(err, ErrGitStatusFilters) {
		t.Fatalf("a probe that could not run must be an error, not a verdict: %v", err)
	}
}

// TestRunGitTreeProbe_Positive_PinsTheLineEndingModel: every tree probe reads the repository
// under core.autocrlf=input, whatever the repository itself sets.
func TestRunGitTreeProbe_Positive_PinsTheLineEndingModel(t *testing.T) {
	dir := statusRepo(t)
	statusGit(t, dir, "config", "core.autocrlf", "true")
	out, err := RunGitTreeProbe(t.Context(), dir, MaxCommandOutputBytes, GitProbeTimeout, "config", "--get", "core.autocrlf")
	if err != nil || strings.TrimSpace(string(out.Stdout)) != "input" {
		t.Fatalf("core.autocrlf under a tree probe = %q (%v), want input", out.Stdout, err)
	}
}

// TestGitHiddenIndexReason_3D: S and s are skip-worktree, other lowercase tags assume-unchanged,
// and every tag git status compares -- uppercase, and the bytes either side of a..z -- is "".
func TestGitHiddenIndexReason_3D(t *testing.T) {
	cases := map[byte]string{
		'S': "skip-worktree", 's': "skip-worktree", 'h': "assume-unchanged", 'a': "assume-unchanged",
		'z': "assume-unchanged", 'H': "", 'M': "", 'R': "", '`': "", '{': "", 0: "",
	}
	for tag, want := range cases {
		if got := GitHiddenIndexReason(tag); got != want {
			t.Errorf("GitHiddenIndexReason(%q) = %q, want %q", tag, got, want)
		}
	}
}

// TestDescribeWorkingTreeChanges_Boundary_NamesOnlyTheFirstFew: past the bound the summary
// counts the rest; at the bound it lists every change without a remainder.
func TestDescribeWorkingTreeChanges_Boundary_NamesOnlyTheFirstFew(t *testing.T) {
	changes := []string{"?? a", "?? b", "?? c", "?? d", "?? e", "?? f", "?? g"}
	got := DescribeWorkingTreeChanges(changes)
	if !strings.HasPrefix(got, "7 changed path(s)") || !strings.Contains(got, "?? e") ||
		strings.Contains(got, "?? f") || !strings.HasSuffix(got, "and 2 more") {
		t.Fatalf("summary of seven changes = %q", got)
	}
	if got := DescribeWorkingTreeChanges(changes[:maxReportedTreeChanges]); strings.Contains(got, "more") {
		t.Fatalf("exactly the bound must list every change without a remainder: %q", got)
	}
	if got := DescribeWorkingTreeChanges([]string{"?? only"}); got != "1 changed path(s) differ from HEAD: ?? only" {
		t.Fatalf("one change = %q", got)
	}
}

// TestGitLiteralExclude_Boundary_GlobCharactersAreLiteral: the exclude drops exactly the named
// path, so a name holding glob characters does not also hide the files it would match as a glob.
func TestGitLiteralExclude_Boundary_GlobCharactersAreLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows file names cannot hold '*'")
	}
	dir := statusRepo(t)
	writeStatusFile(t, dir, "r*.json", "r\n")
	writeStatusFile(t, dir, "receipt.json", "r\n")
	if got := mustChanges(t, dir, GitLiteralExclude("r*.json")); !slices.Equal(got, []string{"?? receipt.json"}) {
		t.Fatalf("a literal exclude must drop only r*.json: %q", got)
	}
	if got := mustChanges(t, dir, GitLiteralExclude("absent.json")); len(got) != 2 {
		t.Fatalf("excluding an absent path must leave every change: %q", got)
	}
}

// TestGitWorkingTreeChanges_Positive_HonoursTheUserGlobalExcludes: a file that only the
// user's global ignore hides is not a change, exactly as plain `git status` reports it,
// although the probe itself runs with a sealed git environment.
func TestGitWorkingTreeChanges_Positive_HonoursTheUserGlobalExcludes(t *testing.T) {
	dir := statusRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeStatusFile(t, home, filepath.Join(".config", "git", "ignore"), ".DS_Store\n")
	writeStatusFile(t, dir, ".DS_Store", "finder\n")
	if changes := mustChanges(t, dir); len(changes) != 0 {
		t.Fatalf("a globally ignored file was reported as a change: %v", changes)
	}
}

// TestGitWorkingTreeChanges_Negative_WithoutAGlobalIgnoreTheFileIsAChange: the same file
// with no global excludes file anywhere is an untracked change.
func TestGitWorkingTreeChanges_Negative_WithoutAGlobalIgnoreTheFileIsAChange(t *testing.T) {
	dir := statusRepo(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	writeStatusFile(t, dir, ".DS_Store", "finder\n")
	changes := mustChanges(t, dir)
	if len(changes) != 1 || !strings.Contains(changes[0], ".DS_Store") {
		t.Fatalf("an unignored untracked file must be reported, got %v", changes)
	}
}

// TestGitWorkingTreeChanges_Boundary_ConfiguredExcludesFileWins: a core.excludesFile set in
// the user's global config is used instead of the XDG default, and XDG_CONFIG_HOME outranks
// HOME for the default; a directory in place of the file is not an excludes file.
func TestGitWorkingTreeChanges_Boundary_ConfiguredExcludesFileWins(t *testing.T) {
	dir := statusRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	custom := filepath.Join(home, "custom-ignore")
	writeStatusFile(t, home, "custom-ignore", "*.swp\n")
	writeStatusFile(t, home, ".gitconfig", "[core]\n\texcludesFile = "+filepath.ToSlash(custom)+"\n")
	writeStatusFile(t, dir, "notes.swp", "swap\n")
	if changes := mustChanges(t, dir); len(changes) != 0 {
		t.Fatalf("the configured excludes file was not honoured: %v", changes)
	}
	if got := defaultGlobalExcludes("/xdg", "/home/u"); got != filepath.Join("/xdg", "git", "ignore") {
		t.Fatalf("XDG_CONFIG_HOME must win for the default, got %q", got)
	}
	if got := defaultGlobalExcludes("", ""); got != "" {
		t.Fatalf("no XDG and no HOME must yield no default, got %q", got)
	}
	notAFile := t.TempDir()
	t.Setenv("HOME", notAFile)
	if err := os.MkdirAll(filepath.Join(notAFile, ".config", "git", "ignore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if args := globalExcludesArgs(t.Context(), dir); args != nil {
		t.Fatalf("a directory is not an excludes file, got %v", args)
	}
}
