package util

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// statusRepo initialises a work tree holding one committed file at a.txt and one at
// sub/b.txt, or skips the test when git is unavailable (HISS-21: the helper asks git, and a
// host without git cannot answer).
func statusRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
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
	changes, err := GitWorkingTreeChanges(t.Context(), dir, pathspec...)
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
		_, err := GitWorkingTreeChanges(t.Context(), dir)
		if !errors.Is(err, ErrGitStatusFilters) || !strings.Contains(err.Error(), "filter.normalise.clean") {
			t.Fatalf("a configured clean filter was not refused: %v", err)
		}
	})
	t.Run("not a repository", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
		if _, err := GitWorkingTreeChanges(t.Context(), t.TempDir()); err == nil {
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
