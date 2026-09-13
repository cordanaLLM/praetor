package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v, output: %s", strings.Join(args, " "), err, string(out))
		}
	}

	runCmd("init", "-b", "main")
	runCmd("config", "user.name", "Standards Test Agent")
	runCmd("config", "user.email", "agent@cordana.ai")
	runCmd("config", "core.longpaths", "true")

	initFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initFile, []byte("# Root Repository\n"), 0o644); err != nil {
		t.Fatalf("failed creating initial README.md: %v", err)
	}

	runCmd("add", "README.md")
	runCmd("commit", "-m", "initial commit")

	return dir
}

func runInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git in %s failed (%s): %v, output: %s", dir, strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// 1. Positive Tests (HISS-15: Operational Correctness)
// ---------------------------------------------------------------------------

func TestWorktree_Positive_LifecycleAndIsolation(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wtA, err := mgr.Create(ctx, "task-alpha", "main")
	if err != nil || wtA.TaskID != "task-alpha" || wtA.Branch != "wt/task-alpha" {
		t.Fatalf("failed creating task-alpha worktree: %v", err)
	}

	wtB, err := mgr.Create(ctx, "task-beta", "main")
	if err != nil {
		t.Fatalf("failed creating task-beta worktree: %v", err)
	}

	verifyIsolation(t, wtA, wtB)
	verifyListAndRemoval(t, mgr, ctx, wtA, wtB)
}

func verifyIsolation(t *testing.T, wtA, wtB *Worktree) {
	fileA := filepath.Join(wtA.Path, "alpha.txt")
	if err := os.WriteFile(fileA, []byte("alpha content\n"), 0o644); err != nil {
		t.Fatalf("failed writing alpha.txt: %v", err)
	}
	runInDir(t, wtA.Path, "add", "alpha.txt")
	runInDir(t, wtA.Path, "commit", "-m", "commit in alpha")

	fileB := filepath.Join(wtB.Path, "beta.txt")
	if err := os.WriteFile(fileB, []byte("beta content\n"), 0o644); err != nil {
		t.Fatalf("failed writing beta.txt: %v", err)
	}
	runInDir(t, wtB.Path, "add", "beta.txt")
	runInDir(t, wtB.Path, "commit", "-m", "commit in beta")

	if _, err := os.Stat(filepath.Join(wtB.Path, "alpha.txt")); !os.IsNotExist(err) {
		t.Errorf("expected alpha.txt NOT to exist in wtB")
	}
	if _, err := os.Stat(filepath.Join(wtA.Path, "beta.txt")); !os.IsNotExist(err) {
		t.Errorf("expected beta.txt NOT to exist in wtA")
	}
}

func verifyListAndRemoval(t *testing.T, mgr *Manager, ctx context.Context, wtA, wtB *Worktree) {
	list, err := mgr.List(ctx)
	if err != nil || len(list) < 3 {
		t.Fatalf("failed listing worktrees: err=%v len=%d", err, len(list))
	}

	foundAlpha, foundBeta := false, false
	for _, item := range list {
		if item.Branch == "wt/task-alpha" {
			foundAlpha = true
		}
		if item.Branch == "wt/task-beta" {
			foundBeta = true
		}
	}
	if !foundAlpha || !foundBeta {
		t.Errorf("expected both tasks in list: alpha=%v beta=%v", foundAlpha, foundBeta)
	}

	if err := mgr.Remove(ctx, "task-alpha", false); err != nil {
		t.Fatalf("expected clean Remove on task-alpha to succeed: %v", err)
	}
	if _, err := os.Stat(wtA.Path); !os.IsNotExist(err) {
		t.Errorf("expected wtA directory to be deleted after Remove")
	}
	if got := runInDir(t, mgr.RootDir(), "rev-parse", "--verify", wtA.Branch); strings.TrimSpace(got) == "" {
		t.Errorf("expected safe removal to preserve branch %s", wtA.Branch)
	}

	if err := mgr.Remove(ctx, "task-beta", true); err != nil {
		t.Fatalf("expected Remove(force=true) on task-beta to succeed: %v", err)
	}
	if _, err := os.Stat(wtB.Path); !os.IsNotExist(err) {
		t.Errorf("expected wtB directory to be deleted after Remove")
	}
}

func TestWorktree_Positive_SafeRemovalPreservesUnpublishedCommit(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-preserve", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	commitFile := filepath.Join(wt.Path, "unpublished.txt")
	if err := os.WriteFile(commitFile, []byte("unpublished commit\n"), 0o644); err != nil {
		t.Fatalf("failed writing commit file: %v", err)
	}
	runInDir(t, wt.Path, "add", "unpublished.txt")
	runInDir(t, wt.Path, "commit", "-m", "unpublished work")
	commitSHA := strings.TrimSpace(runInDir(t, wt.Path, "rev-parse", "HEAD"))

	if err := mgr.Remove(ctx, wt.TaskID, false); err != nil {
		t.Fatalf("safe removal failed: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to be removed, stat error: %v", err)
	}
	gotSHA := strings.TrimSpace(runInDir(t, repoDir, "rev-parse", "--verify", wt.Branch))
	if gotSHA != commitSHA {
		t.Fatalf("safe removal lost unpublished branch commit: got %s, want %s", gotSHA, commitSHA)
	}
}

func TestWorktree_Negative_ReleasedRemovalRejectsIgnoredOnlyFiles(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()
	wt, err := mgr.Create(ctx, "task-ignored-only", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("failed creating gitignore: %v", err)
	}
	runInDir(t, wt.Path, "add", ".gitignore")
	runInDir(t, wt.Path, "commit", "-m", "ignore disposable files")
	ignored := filepath.Join(wt.Path, "ignored", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(ignored), 0o755); err != nil {
		t.Fatalf("failed creating ignored directory: %v", err)
	}
	if err := os.WriteFile(ignored, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("failed creating ignored file: %v", err)
	}
	if err := mgr.CheckRemoval(ctx, wt.Path); err == nil || !strings.Contains(err.Error(), "ignored files") {
		t.Fatalf("expected ignored-only removal refusal, got %v", err)
	}
	if err := mgr.RemoveReleased(ctx, wt.Path); err == nil {
		t.Fatal("expected released removal to refuse ignored-only worktree")
	}
	if _, err := os.Stat(ignored); err != nil {
		t.Fatalf("ignored file should survive refusal: %v", err)
	}
}

func TestWorktree_Negative_ReleasedRemovalRejectsUnownedPrimaryAndDetached(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()
	if err := mgr.CheckRemoval(ctx, repoDir); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("expected primary worktree refusal, got %v", err)
	}

	foreignDir := setupTestGitRepo(t)
	if err := mgr.CheckRemoval(ctx, foreignDir); err == nil || !strings.Contains(err.Error(), "not a registered worktree") {
		t.Fatalf("expected foreign worktree refusal, got %v", err)
	}

	detachedPath := filepath.Join(repoDir, "detached-worktree")
	runInDir(t, repoDir, "worktree", "add", "--detach", detachedPath, "main")
	defer func() { runInDir(t, repoDir, "worktree", "remove", "--force", detachedPath) }()
	if err := mgr.CheckRemoval(ctx, detachedPath); err == nil || !strings.Contains(err.Error(), "unsafe registered worktree") {
		t.Fatalf("expected detached worktree refusal, got %v", err)
	}
}

func TestWorktree_Negative_ReleasedRemovalRejectsSymlinkAlias(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()
	wt, err := mgr.Create(ctx, "task-symlink", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	alias := filepath.Join(repoDir, ".standards", "worktrees", "task-symlink-alias")
	if err := os.Symlink(wt.Path, alias); err != nil {
		t.Fatalf("failed creating worktree alias: %v", err)
	}
	if err := mgr.CheckRemoval(ctx, alias); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected symlink alias refusal, got %v", err)
	}
	if err := mgr.RemoveReleased(ctx, alias); err == nil {
		t.Fatal("expected released removal to refuse symlink alias")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("registered worktree should survive alias refusal: %v", err)
	}
}

func TestWorktree_Positive_MutatingGitIgnoresAmbientRepositoryRedirect(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	foreignDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()
	wt, err := mgr.Create(ctx, "task-ambient-git", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(foreignDir, ".git"))
	if err := mgr.Remove(ctx, wt.TaskID, false); err != nil {
		t.Fatalf("mutating removal should ignore ambient GIT_DIR: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree removal, stat error: %v", err)
	}
}

func TestWorktree_Negative_ReleasedProbeRejectsAmbientFilterAndFsmonitor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		marker string
		want   string
		attrs  bool
	}{
		{name: "filter", config: "[filter \"malicious\"]\n\tclean = !touch MARKER\n", marker: "filter marker", want: "configured filters", attrs: true},
		{name: "fsmonitor", config: "[core]\n\tfsmonitor = !touch MARKER\n", marker: "fsmonitor marker", want: "attributes, excludes, or fsmonitor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := setupTestGitRepo(t)
			mgr := NewManager(repoDir)
			ctx := context.Background()
			wt, err := mgr.Create(ctx, "task-ambient-"+tc.name, "main")
			if err != nil {
				t.Fatalf("failed creating worktree: %v", err)
			}
			if tc.attrs {
				if err := os.WriteFile(filepath.Join(wt.Path, ".gitattributes"), []byte("*.txt filter=malicious\n"), 0o644); err != nil {
					t.Fatalf("failed writing attributes: %v", err)
				}
				if err := os.WriteFile(filepath.Join(wt.Path, "sample.txt"), []byte("sample\n"), 0o644); err != nil {
					t.Fatalf("failed writing filtered file: %v", err)
				}
				runInDir(t, wt.Path, "add", ".gitattributes")
				runInDir(t, wt.Path, "add", "sample.txt")
				runInDir(t, wt.Path, "commit", "-m", "configure filter attribute")
			}
			home := t.TempDir()
			marker := filepath.Join(home, tc.marker)
			config := strings.ReplaceAll(tc.config, "MARKER", marker)
			if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0o600); err != nil {
				t.Fatalf("failed writing global Git config: %v", err)
			}
			t.Setenv("HOME", home)
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
			if err := mgr.CheckRemoval(ctx, wt.Path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected ambient %s refusal, got %v", tc.name, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("ambient %s command executed unexpectedly: %v", tc.name, err)
			}
		})
	}
}

func TestWorktree_Positive_DirtyWorktreeForceRemoval(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-dirty", "main")
	if err != nil {
		t.Fatalf("failed creating task-dirty: %v", err)
	}

	// Commit the ignore rule so the fixture exercises both untracked and ignored
	// content during the safe-removal refusal.
	if err := os.WriteFile(filepath.Join(wt.Path, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("failed creating gitignore: %v", err)
	}
	runInDir(t, wt.Path, "add", ".gitignore")
	runInDir(t, wt.Path, "commit", "-m", "ignore disposable files")

	// Create untracked file to make the worktree dirty.
	dirtyFile := filepath.Join(wt.Path, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted change\n"), 0o644); err != nil {
		t.Fatalf("failed creating dirty file: %v", err)
	}
	ignoredDir := filepath.Join(wt.Path, "ignored")
	if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
		t.Fatalf("failed creating ignored directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "secret.txt"), []byte("ignored change\n"), 0o644); err != nil {
		t.Fatalf("failed creating ignored file: %v", err)
	}
	if ignored := runInDir(t, wt.Path, "check-ignore", "ignored/secret.txt"); strings.TrimSpace(ignored) == "" {
		t.Fatal("fixture file should be ignored by the committed ignore rule")
	}

	// Non-force remove must fail because worktree contains untracked files
	if err := mgr.Remove(ctx, "task-dirty", false); err == nil {
		t.Fatalf("expected non-force Remove on dirty worktree to fail, but it succeeded")
	}

	// Workspace and branch must still exist
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Errorf("worktree path should still exist after failed safe removal")
	}
	if _, err := os.Stat(filepath.Join(ignoredDir, "secret.txt")); err != nil {
		t.Errorf("ignored file should survive failed safe removal: %v", err)
	}

	// Force remove must succeed
	if err := mgr.Remove(ctx, "task-dirty", true); err != nil {
		t.Fatalf("expected force Remove to succeed on dirty worktree: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory should be deleted after force Remove")
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "wt/task-dirty")
	cmd.Dir = repoDir
	if cmd.Run() == nil {
		t.Errorf("force removal should delete managed branch wt/task-dirty")
	}
}

func TestWorktree_Negative_ForceRemovalReportsBranchCleanupFailure(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-missing-branch", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	// Remove the ref behind Git's back so worktree removal can succeed while
	// the explicit force cleanup has a precise, observable failure.
	runInDir(t, repoDir, "update-ref", "-d", "refs/heads/"+wt.Branch)

	err = mgr.Remove(ctx, wt.TaskID, true)
	if err == nil {
		t.Fatal("expected force removal to report missing branch cleanup")
	}
	if !strings.Contains(err.Error(), "worktree removed") || !strings.Contains(err.Error(), "failed deleting branch wt/task-missing-branch") {
		t.Fatalf("force cleanup error does not describe both outcomes: %v", err)
	}
	if _, statErr := os.Stat(wt.Path); !os.IsNotExist(statErr) {
		t.Fatalf("worktree should be removed despite branch cleanup failure, stat error: %v", statErr)
	}
}

func TestWorktree_Positive_Prune(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-prune", "main")
	if err != nil {
		t.Fatalf("failed creating task-prune: %v", err)
	}

	// Simulate worktree directory getting deleted out-of-band
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatalf("failed deleting worktree dir: %v", err)
	}

	// Check that git reports it as prunable
	list, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("failed listing worktrees: %v", err)
	}

	foundPrunable := false
	for i := 0; i < len(list); i++ {
		if list[i].Branch == "wt/task-prune" && list[i].Prunable {
			foundPrunable = true
			break
		}
	}
	if !foundPrunable {
		t.Errorf("expected task-prune to be marked prunable in list")
	}

	// Execute Prune
	if err := mgr.Prune(ctx); err != nil {
		t.Fatalf("failed Prune: %v", err)
	}

	// After prune, it should no longer be listed
	listAfter, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("failed listing worktrees after prune: %v", err)
	}
	for i := 0; i < len(listAfter); i++ {
		if listAfter[i].Branch == "wt/task-prune" {
			t.Errorf("expected pruned worktree to no longer appear in worktree list")
		}
	}
}

func TestWorktree_Positive_LockedWorktree(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-locked", "main")
	if err != nil {
		t.Fatalf("failed creating task-locked: %v", err)
	}

	runInDir(t, repoDir, "worktree", "lock", "--reason", "agent-in-progress", wt.Path)

	list, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("failed listing worktrees: %v", err)
	}

	foundLocked := false
	for i := 0; i < len(list); i++ {
		if list[i].Branch == "wt/task-locked" {
			if list[i].Locked && list[i].LockReason == "agent-in-progress" {
				foundLocked = true
			}
		}
	}
	if !foundLocked {
		t.Errorf("expected task-locked worktree with reason 'agent-in-progress'")
	}

	// Git prohibits removing locked worktrees even with single --force; unlock first
	runInDir(t, repoDir, "worktree", "unlock", wt.Path)

	if err := mgr.Remove(ctx, "task-locked", true); err != nil {
		t.Fatalf("expected Remove(force=true) to succeed after unlock: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2. Negative Tests (HISS-15: Error Handling & Invariant Enforcement)
// ---------------------------------------------------------------------------

func TestWorktree_Negative_ValidationErrors(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	// Empty taskID
	if _, err := mgr.Create(ctx, "", "main"); !errors.Is(err, ErrEmptyTaskID) {
		t.Errorf("expected ErrEmptyTaskID, got %v", err)
	}

	// Invalid characters in taskID
	invalidIDs := []string{
		"task with space",
		"task/slash",
		"task\\backslash",
		"../escape",
		"task@symbol",
		"task:colon",
	}
	for i := 0; i < len(invalidIDs); i++ {
		if _, err := mgr.Create(ctx, invalidIDs[i], "main"); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("expected ErrInvalidTaskID for %q, got %v", invalidIDs[i], err)
		}
		if err := mgr.Remove(ctx, invalidIDs[i], false); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("expected ErrInvalidTaskID on Remove for %q, got %v", invalidIDs[i], err)
		}
	}

	// Empty base branch
	if _, err := mgr.Create(ctx, "valid-task", ""); !errors.Is(err, ErrEmptyBaseBranch) {
		t.Errorf("expected ErrEmptyBaseBranch, got %v", err)
	}

	// Invalid base branch characters
	invalidBranches := []string{
		"branch with space",
		"branch..double-dot",
		"branch~1",
		"branch^2",
		"branch:colon",
		"branch?glob",
		"branch*star",
	}
	for i := 0; i < len(invalidBranches); i++ {
		if _, err := mgr.Create(ctx, "valid-task", invalidBranches[i]); !errors.Is(err, ErrInvalidBaseBranch) {
			t.Errorf("expected ErrInvalidBaseBranch for %q, got %v", invalidBranches[i], err)
		}
	}
}

func TestWorktree_Negative_NonExistentBaseBranch(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	if _, err := mgr.Create(ctx, "task-bad-base", "non-existent-branch"); err == nil {
		t.Fatalf("expected error creating worktree from non-existent branch, but got nil")
	}
}

func TestWorktree_Negative_DuplicateWorktree(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	if _, err := mgr.Create(ctx, "task-dup", "main"); err != nil {
		t.Fatalf("initial create failed: %v", err)
	}
	defer func() {
		if err := mgr.Remove(ctx, "task-dup", true); err != nil {
			t.Errorf("cleanup duplicate worktree: %v", err)
		}
	}()

	// Re-creating the same task worktree must fail
	if _, err := mgr.Create(ctx, "task-dup", "main"); err == nil {
		t.Fatalf("expected duplicate Create to fail, but it succeeded")
	}
}

func TestWorktree_Negative_RemoveNonExistent(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	if err := mgr.Remove(ctx, "task-does-not-exist", false); err == nil {
		t.Fatalf("expected Remove on non-existent worktree to fail, but got nil")
	}
}

func TestWorktree_Negative_NilContext(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)

	var nilCtx context.Context
	if _, err := mgr.Create(nilCtx, "task-1", "main"); !errors.Is(err, ErrNilContext) {
		t.Errorf("expected ErrNilContext for Create, got %v", err)
	}
	if err := mgr.Remove(nilCtx, "task-1", false); !errors.Is(err, ErrNilContext) {
		t.Errorf("expected ErrNilContext for Remove, got %v", err)
	}
	if _, err := mgr.List(nilCtx); !errors.Is(err, ErrNilContext) {
		t.Errorf("expected ErrNilContext for List, got %v", err)
	}
	if err := mgr.Prune(nilCtx); !errors.Is(err, ErrNilContext) {
		t.Errorf("expected ErrNilContext for Prune, got %v", err)
	}
}

func TestWorktree_Negative_CancelledContext(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if _, err := mgr.Create(ctx, "task-cancelled", "main"); err == nil {
		t.Errorf("expected context cancellation error on Create, got nil")
	}
	if err := mgr.Remove(ctx, "task-cancelled", false); err == nil {
		t.Errorf("expected context cancellation error on Remove, got nil")
	}
	if _, err := mgr.List(ctx); err == nil {
		t.Errorf("expected context cancellation error on List, got nil")
	}
	if err := mgr.Prune(ctx); err == nil {
		t.Errorf("expected context cancellation error on Prune, got nil")
	}
}

func TestWorktree_Negative_NilManager(t *testing.T) {
	var mgr *Manager
	ctx := context.Background()

	if _, err := mgr.Create(ctx, "task-1", "main"); !errors.Is(err, ErrNilManager) {
		t.Errorf("expected ErrNilManager for Create, got %v", err)
	}
	if err := mgr.Remove(ctx, "task-1", false); !errors.Is(err, ErrNilManager) {
		t.Errorf("expected ErrNilManager for Remove, got %v", err)
	}
	if _, err := mgr.List(ctx); !errors.Is(err, ErrNilManager) {
		t.Errorf("expected ErrNilManager for List, got %v", err)
	}
	if err := mgr.Prune(ctx); !errors.Is(err, ErrNilManager) {
		t.Errorf("expected ErrNilManager for Prune, got %v", err)
	}
	if mgr.RootDir() != "" {
		t.Errorf("expected empty RootDir for nil manager")
	}
}

// ---------------------------------------------------------------------------
// 3. Boundary Tests (HISS-15: Numeric, String & Buffer Limits)
// ---------------------------------------------------------------------------

func TestWorktree_Boundary_TaskIDLengths(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	// Min length (1 char)
	wtMin, err := mgr.Create(ctx, "a", "main")
	if err != nil {
		t.Fatalf("expected 1-char taskID to succeed: %v", err)
	}
	if err := mgr.Remove(ctx, "a", true); err != nil {
		t.Fatalf("cleanup for 1-char taskID failed: %v", err)
	}
	if wtMin.TaskID != "a" {
		t.Errorf("expected TaskID 'a', got %s", wtMin.TaskID)
	}

	// Max length (128 chars)
	maxID := strings.Repeat("x", MaxTaskIDLength)
	wtMax, err := mgr.Create(ctx, maxID, "main")
	if err != nil && os.PathSeparator == '\\' && strings.Contains(err.Error(), "$GIT_DIR' too big") {
		// Windows Git setup.c PATH_MAX (260 byte) limit hit due to deep %TEMP% path; verify with safe length on Windows
		maxID = strings.Repeat("x", 48)
		wtMax, err = mgr.Create(ctx, maxID, "main")
	}
	if err != nil {
		t.Fatalf("expected max-length taskID to succeed: %v", err)
	}
	if err := mgr.Remove(ctx, maxID, true); err != nil {
		t.Fatalf("cleanup for max-length taskID failed: %v", err)
	}
	if wtMax.TaskID != maxID {
		t.Errorf("expected TaskID %s, got %s", maxID, wtMax.TaskID)
	}

	// Exceeded length (129 chars)
	tooLongID := strings.Repeat("x", MaxTaskIDLength+1)
	if _, err := mgr.Create(ctx, tooLongID, "main"); !errors.Is(err, ErrTaskIDTooLong) {
		t.Errorf("expected ErrTaskIDTooLong for %d chars, got %v", MaxTaskIDLength+1, err)
	}
}

func TestWorktree_Boundary_ParseWorktreeListVariations(t *testing.T) {
	// 1. Empty string
	emptyRes, err := parseWorktreeList("")
	if err != nil {
		t.Fatalf("unexpected error parsing empty string: %v", err)
	}
	if len(emptyRes) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(emptyRes))
	}

	// 2. Whitespace and empty lines only
	wsRes, err := parseWorktreeList("   \n\n\n  \t \n")
	if err != nil {
		t.Fatalf("unexpected error parsing whitespace: %v", err)
	}
	if len(wsRes) != 0 {
		t.Errorf("expected 0 results for whitespace input, got %d", len(wsRes))
	}

	// 3. Detached HEAD and bare entries
	input := `worktree /path/to/bare
bare

worktree /path/to/detached
HEAD 1234567890abcdef1234567890abcdef12345678
detached

worktree /path/to/locked-no-reason
HEAD abcdef1234567890abcdef1234567890abcdef12
branch refs/heads/wt/task-locked
locked

worktree /path/to/prunable-no-reason
HEAD abcdef1234567890abcdef1234567890abcdef12
branch refs/heads/wt/task-prunable
prunable
`
	parsed, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("failed parsing variations: %v", err)
	}
	verifyParsedWorktreeVariations(t, parsed)

	// 4. Exceeding MaxPorcelainLines bound
	excessiveLines := strings.Repeat("worktree /foo\nHEAD 123\n\n", (MaxPorcelainLines/3)+10)
	if _, err := parseWorktreeList(excessiveLines); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded when line count exceeds %d, got %v", MaxPorcelainLines, err)
	}
}

func verifyParsedWorktreeVariations(t *testing.T, parsed []WorktreeInfo) {
	if len(parsed) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(parsed))
	}
	if !parsed[0].Bare || parsed[0].Path != "/path/to/bare" {
		t.Errorf("entry 0 bare mismatch: %+v", parsed[0])
	}
	if !parsed[1].Detached || parsed[1].HEAD != "1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("entry 1 detached mismatch: %+v", parsed[1])
	}
	if !parsed[2].Locked || parsed[2].LockReason != "" || parsed[2].Branch != "wt/task-locked" {
		t.Errorf("entry 2 locked mismatch: %+v", parsed[2])
	}
	if !parsed[3].Prunable || parsed[3].PruneReason != "" || parsed[3].Branch != "wt/task-prunable" {
		t.Errorf("entry 3 prunable mismatch: %+v", parsed[3])
	}
}

func TestWorktree_Boundary_ManagerPathNormalization(t *testing.T) {
	mEmpty := NewManager("")
	if mEmpty.RootDir() != "." {
		t.Errorf("expected '.' for empty rootDir, got %q", mEmpty.RootDir())
	}

	mSlash := NewManager("/tmp/foo/bar///")
	expectedClean := filepath.Clean("/tmp/foo/bar")
	if mSlash.RootDir() != expectedClean {
		t.Errorf("expected %q, got %q", expectedClean, mSlash.RootDir())
	}

	var nilMgr *Manager
	defaultPath := nilMgr.WorktreePath("task-99")
	if !strings.HasSuffix(defaultPath, filepath.Join(".standards", "worktrees", "task-99")) {
		t.Errorf("unexpected fallback path for nil manager: %s", defaultPath)
	}
}
