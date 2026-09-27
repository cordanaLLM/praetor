package state

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecordedCommit_3D pins the commit every archive and baseline record is stamped with:
// HEAD's SHA, "" where no commit exists to name, and an error where Git did not answer.
func TestRecordedCommit_3D(t *testing.T) {
	// Positive: a committed repository names its HEAD.
	repo := t.TempDir()
	stateFixtureGit(t, repo, "init", "-b", "main")
	stateFixtureGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	want, err := stateGitString(t.Context(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := RecordedCommit(t.Context(), repo); err != nil || got != want {
		t.Fatalf("committed repository = %q, %v; want %q", got, err, want)
	}

	// Boundary: an unborn branch and a directory outside any worktree have no commit to name,
	// and the unborn marker never leaks into a record.
	unborn := t.TempDir()
	stateFixtureGit(t, unborn, "init", "-b", "main")
	for name, dir := range map[string]string{"unborn": unborn, "non-git": t.TempDir()} {
		if got, err := RecordedCommit(t.Context(), dir); err != nil || got != "" {
			t.Fatalf("%s = %q, %v; want no commit and no error", name, got, err)
		}
	}

	// Negative: broken Git metadata and a cancelled context are errors, not an empty commit.
	broken := t.TempDir()
	writeIntegrityFile(t, filepath.Join(broken, ".git"), "gitdir: nonexistent\n")
	if got, err := RecordedCommit(t.Context(), broken); err == nil || !strings.Contains(err.Error(), "read Git HEAD") {
		t.Fatalf("broken metadata = %q, %v; want a Git HEAD error", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := RecordedCommit(ctx, repo); err == nil {
		t.Fatalf("cancelled context = %q; want an error", got)
	}
}
