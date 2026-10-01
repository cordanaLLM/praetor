package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = testsupport.HermeticGitEnv(t)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v, output: %s", strings.Join(args, " "), err, string(out))
		}
	}

	runCmd("init", "-b", "main")
	runCmd("config", "user.name", "Standards Test Agent")
	runCmd("config", "user.email", "agent@example.com")
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
	cmd.Env = testsupport.HermeticGitEnv(t)
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

// operatorHome gives the test process a fresh home: HOME, USERPROFILE and XDG_CONFIG_HOME point
// into it and GIT_CONFIG_GLOBAL names its .gitconfig, which holds config. Every "MARKER:<name>"
// in config becomes a quoted path in the home, returned by name, so a test can tell whether git
// ran the command holding it.
//
// The marker is single-quoted for the shell git runs the command in, and written with forward
// slashes. Unquoted, a space in the path split it into two arguments, so an executed command
// touched two other files and no marker check could fail; with backslashes, git read a Windows
// path as escape sequences and refused the configuration. A leading "!" is alias syntax that
// git runs as a command named "!touch", which can never create a marker either.
func operatorHome(t *testing.T, config string, markers ...string) (home string, paths map[string]string) {
	t.Helper()
	home = t.TempDir()
	paths = make(map[string]string, len(markers))
	for _, name := range markers {
		paths[name] = filepath.Join(home, name+" marker")
	}
	writeTestFile(t, filepath.Join(home, ".gitconfig"), expandMarkers(config, paths))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	return home, paths
}

// expandMarkers replaces each "MARKER:<name>" in config with the quoted path paths names.
func expandMarkers(config string, paths map[string]string) string {
	for name, path := range paths {
		config = strings.ReplaceAll(config, "MARKER:"+name, "'"+filepath.ToSlash(path)+"'")
	}
	return config
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed writing %s: %v", path, err)
	}
}

// commitSample commits sample.txt, and .gitattributes when attributes is set, in the worktree
// before any operator configuration exists, then ages the file so the next status cannot trust
// the index's stat data and must read, and clean, its content.
func commitSample(t *testing.T, wtPath, attributes string) {
	t.Helper()
	if attributes != "" {
		writeTestFile(t, filepath.Join(wtPath, ".gitattributes"), attributes)
	}
	writeTestFile(t, filepath.Join(wtPath, "sample.txt"), "sample\n")
	runInDir(t, wtPath, "add", "-A")
	runInDir(t, wtPath, "commit", "-m", "add sample")
	old := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(wtPath, "sample.txt"), old, old); err != nil {
		t.Fatalf("failed ageing sample.txt: %v", err)
	}
}

func assertNoMarker(t *testing.T, markers map[string]string) {
	t.Helper()
	for name, path := range markers {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the %s command ran: %v", name, err)
		}
	}
}

// markerDriver is a filter driver block whose clean, process and smudge commands each create
// the "filter" marker.
const markerDriver = "clean = touch MARKER:filter\n\tprocess = touch MARKER:filter\n\tsmudge = touch MARKER:filter\n"

// TestWorktree_Negative_ReleasedRemovalRefusesSelectedOperatorFilter: 'git worktree remove'
// checks the tree with a status that reads the operator's global and system files, so a
// driver defined there, or selected by the global attributes file, would run during the
// removal. Each is refused by the shared check before the removal starts, and nothing runs.
func TestWorktree_Negative_ReleasedRemovalRefusesSelectedOperatorFilter(t *testing.T) {
	selection := "*.txt filter=malicious\n"
	for _, tc := range []struct {
		name       string
		inSystem   bool
		attributes string // "tree", "default" or "configured"
	}{
		{name: "global driver, tree attributes", attributes: "tree"},
		{name: "system driver, tree attributes", inSystem: true, attributes: "tree"},
		{name: "global driver, default global attributes", attributes: "default"},
		{name: "global driver, configured global attributes", attributes: "configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := setupTestGitRepo(t)
			mgr := NewManager(repoDir)
			wt, err := mgr.Create(t.Context(), "task-operator-filter", "main")
			if err != nil {
				t.Fatalf("failed creating worktree: %v", err)
			}
			treeAttributes := ""
			if tc.attributes == "tree" {
				treeAttributes = selection
			}
			commitSample(t, wt.Path, treeAttributes)
			driver, global := "[filter \"malicious\"]\n\t"+markerDriver, ""
			if !tc.inSystem {
				global = driver
			}
			home, markers := operatorHome(t, global, "filter")
			selectOperatorFilter(t, home, tc.attributes, selection)
			if tc.inSystem {
				system := filepath.Join(home, "system-gitconfig")
				writeTestFile(t, system, expandMarkers(driver, markers))
				t.Setenv("GIT_CONFIG_NOSYSTEM", "")
				t.Setenv("GIT_CONFIG_SYSTEM", system)
			}
			err = mgr.CheckRemoval(t.Context(), wt.Path)
			if !errors.Is(err, util.ErrGitStatusFilters) || !strings.Contains(err.Error(), "filter.malicious.clean (filter=malicious on sample.txt)") {
				t.Fatalf("expected the selected operator driver to be refused by name, got %v", err)
			}
			if err := mgr.RemoveReleased(t.Context(), wt.Path); !errors.Is(err, util.ErrGitStatusFilters) {
				t.Fatalf("expected released removal to refuse the selected operator driver, got %v", err)
			}
			if _, err := os.Stat(wt.Path); err != nil {
				t.Fatalf("worktree should survive the refusal: %v", err)
			}
			assertNoMarker(t, markers)
		})
	}
}

// selectOperatorFilter writes selection into the operator-level attributes file where names:
// the default global attributes file, or one core.attributesFile names in the global
// configuration. "tree" leaves the selection to the committed .gitattributes.
func selectOperatorFilter(t *testing.T, home, where, selection string) {
	t.Helper()
	switch where {
	case "default":
		writeTestFile(t, filepath.Join(home, ".config", "git", "attributes"), selection)
	case "configured":
		configureOperatorFile(t, home, "core.attributesFile", selection)
	}
}

// TestWorktree_Negative_ReleasedRemovalRefusesRepositoryFilterOperatorFilesHide: removal runs
// two statuses, git's own under the operator's files and the cleanliness probe without them, so
// a driver selected in either view would run. Operator files can hide a selection the probe
// still makes: a global macro that unsets filter on the line selecting it, or a global
// attr.tree that reads attributes from HEAD while the tree's .gitattributes selects the driver.
// The repository-only view is checked too, so both are refused before any status runs.
func TestWorktree_Negative_ReleasedRemovalRefusesRepositoryFilterOperatorFilesHide(t *testing.T) {
	for _, tc := range []struct {
		name, committed, uncommitted, global, globalAttributes string
	}{
		{
			name:      "global macro unsets the filter",
			committed: "*.txt filter=repo nofilt\n", globalAttributes: "[attr]nofilt -filter\n",
		},
		{
			name:        "global attr.tree reads attributes from HEAD",
			uncommitted: "*.txt filter=repo\n", global: "[attr]\n\ttree = HEAD\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := setupTestGitRepo(t)
			mgr := NewManager(repoDir)
			wt, err := mgr.Create(t.Context(), "task-hidden-filter", "main")
			if err != nil {
				t.Fatalf("failed creating worktree: %v", err)
			}
			commitSample(t, wt.Path, tc.committed)
			home, markers := operatorHome(t, tc.global, "filter")
			selectOperatorFilter(t, home, "default", tc.globalAttributes)
			if tc.uncommitted != "" {
				writeTestFile(t, filepath.Join(wt.Path, ".gitattributes"), tc.uncommitted)
			}
			runInDir(t, wt.Path, "config", "filter.repo.clean", expandMarkers("touch MARKER:filter", markers))
			err = mgr.CheckRemoval(t.Context(), wt.Path)
			if !errors.Is(err, util.ErrGitStatusFilters) || !strings.Contains(err.Error(), "filter.repo.clean (filter=repo on sample.txt)") {
				t.Fatalf("expected the repository driver the probe would run to be refused by name, got %v", err)
			}
			if err := mgr.RemoveReleased(t.Context(), wt.Path); !errors.Is(err, util.ErrGitStatusFilters) {
				t.Fatalf("expected released removal to refuse the repository driver, got %v", err)
			}
			if _, err := os.Stat(wt.Path); err != nil {
				t.Fatalf("worktree should survive the refusal: %v", err)
			}
			assertNoMarker(t, markers)
		})
	}
}

// stockLFSDriver is the filter.lfs block stock Git for Windows defines in its system
// configuration and `git lfs install` writes into the global one.
const stockLFSDriver = "[filter \"lfs\"]\n" +
	"\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n"

// TestWorktree_Positive_ReleasedRemovalIgnoresUnusedOperatorConfig: operator configuration that
// runs nothing during removal no longer protects the worktree (#679): the stock filter.lfs block
// and a marker driver no tracked path selects, a global attributes file that selects no driver,
// a global excludes file and an fsmonitor hook, each alone and all together. The removal
// completes, keeps the branch, and runs none of the commands.
func TestWorktree_Positive_ReleasedRemovalIgnoresUnusedOperatorConfig(t *testing.T) {
	unused := "[filter \"unused\"]\n\t" + markerDriver
	fsmonitor := "[core]\n\tfsmonitor = touch MARKER:fsmonitor\n"
	for _, tc := range []struct {
		name, config, attributes, excludes string
	}{
		{name: "stock filter.lfs block", config: stockLFSDriver},
		{name: "unselected marker driver", config: unused},
		{name: "global attributes without a driver", attributes: "*.txt text eol=lf\n"},
		{name: "global excludes file", excludes: "*.log\n"},
		{name: "fsmonitor hook", config: fsmonitor},
		{name: "all together", config: stockLFSDriver + unused + fsmonitor, attributes: "*.txt text eol=lf\n", excludes: "*.log\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := setupTestGitRepo(t)
			mgr := NewManager(repoDir)
			wt, err := mgr.Create(t.Context(), "task-operator-config", "main")
			if err != nil {
				t.Fatalf("failed creating worktree: %v", err)
			}
			commitSample(t, wt.Path, "")
			home, markers := operatorHome(t, tc.config, "filter", "fsmonitor")
			configureOperatorFile(t, home, "core.attributesFile", tc.attributes)
			configureOperatorFile(t, home, "core.excludesFile", tc.excludes)
			if err := mgr.CheckRemoval(t.Context(), wt.Path); err != nil {
				t.Fatalf("operator configuration that runs nothing refused removal: %v", err)
			}
			if err := mgr.RemoveReleased(t.Context(), wt.Path); err != nil {
				t.Fatalf("released removal failed: %v", err)
			}
			if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected the worktree directory to be removed, stat error: %v", err)
			}
			assertNoMarker(t, markers)
			runInDir(t, repoDir, "rev-parse", "--verify", wt.Branch)
		})
	}
}

// configureOperatorFile writes content to a file in home and names it as key in the global
// configuration; empty content configures nothing.
func configureOperatorFile(t *testing.T, home, key, content string) {
	t.Helper()
	if content == "" {
		return
	}
	file := filepath.Join(home, "operator-"+key)
	writeTestFile(t, file, content)
	runInDir(t, home, "config", "--file", filepath.Join(home, ".gitconfig"), key, filepath.ToSlash(file))
}

// TestWorktree_Boundary_ReleasedRemovalOperatorEdges: a smudge driver a tracked path selects
// passes, since removal checks nothing out, and the removal never runs it; a file only the
// operator's global excludes ignore is still listed by the cleanliness probe, which reads no
// operator file, so the removal is refused and the file survives.
func TestWorktree_Boundary_ReleasedRemovalOperatorEdges(t *testing.T) {
	t.Run("selected smudge-only driver", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		mgr := NewManager(repoDir)
		wt, err := mgr.Create(t.Context(), "task-operator-smudge", "main")
		if err != nil {
			t.Fatalf("failed creating worktree: %v", err)
		}
		commitSample(t, wt.Path, "*.txt filter=checkout\n")
		_, markers := operatorHome(t, "[filter \"checkout\"]\n\tsmudge = touch MARKER:smudge\n", "smudge")
		if err := mgr.RemoveReleased(t.Context(), wt.Path); err != nil {
			t.Fatalf("a smudge-only driver refused removal: %v", err)
		}
		if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected the worktree directory to be removed, stat error: %v", err)
		}
		assertNoMarker(t, markers)
	})
	t.Run("file only the global excludes ignore", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		mgr := NewManager(repoDir)
		wt, err := mgr.Create(t.Context(), "task-operator-excludes", "main")
		if err != nil {
			t.Fatalf("failed creating worktree: %v", err)
		}
		home, _ := operatorHome(t, "")
		writeTestFile(t, filepath.Join(home, ".config", "git", "ignore"), "*.log\n")
		kept := filepath.Join(wt.Path, "notes.log")
		writeTestFile(t, kept, "keep me\n")
		if err := mgr.CheckRemoval(t.Context(), wt.Path); err == nil || !strings.Contains(err.Error(), "untracked files are present") {
			t.Fatalf("expected a globally ignored file to refuse removal, got %v", err)
		}
		if err := mgr.RemoveReleased(t.Context(), wt.Path); err == nil {
			t.Fatal("expected released removal to refuse a globally ignored file")
		}
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("the globally ignored file should survive the refusal: %v", err)
		}
	})
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

	// Git removes a locked worktree only when --force is given twice. The CLI promises
	// forced removal of a locked worktree, so Remove(force) must succeed without an unlock.
	if err := mgr.Remove(ctx, "task-locked", true); err != nil {
		t.Fatalf("expected Remove(force=true) to remove a locked worktree: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("locked worktree directory should be gone after forced removal, stat error: %v", err)
	}
	if exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "--quiet", wt.Branch).Run() == nil {
		t.Fatalf("forced removal of a locked worktree should delete branch %s", wt.Branch)
	}
}

// Safe removal keeps its refusal of a locked worktree: only an explicit force overrides a lock.
func TestWorktree_Negative_SafeRemovalRefusesLockedWorktree(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-locked-safe", "main")
	if err != nil {
		t.Fatalf("failed creating worktree: %v", err)
	}
	runInDir(t, repoDir, "worktree", "lock", "--reason", "agent-in-progress", wt.Path)

	if err := mgr.Remove(ctx, wt.TaskID, false); err == nil {
		t.Fatal("safe removal of a locked worktree must be refused")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("refused removal must leave the locked worktree in place: %v", err)
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
		// Option-like values: git would parse them as flags and branch from HEAD.
		"--force",
		"-x",
		"-",
		// Padded values: validation must judge the value git receives, not a trimmed copy.
		" main",
		"main ",
		"\tmain",
		"main\n",
		"main\x00",
		"main\x7f",
		"branch\\backslash",
	}
	for i := 0; i < len(invalidBranches); i++ {
		if _, err := mgr.Create(ctx, "valid-task", invalidBranches[i]); !errors.Is(err, ErrInvalidBaseBranch) {
			t.Errorf("expected ErrInvalidBaseBranch for %q, got %v", invalidBranches[i], err)
		}
	}
	list, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("a rejected base branch must create no worktree, got %+v", list)
	}
}

// TestWorktree_Positive_HyphenatedBaseBranch pins BUG-240: the old character set carried a
// "\x00-\x1f" span that ContainsAny reads as three characters, one of them '-', so every
// hyphenated base branch was refused. Every other Create in this file uses "main".
func TestWorktree_Positive_HyphenatedBaseBranch(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	for _, base := range []string{"release-1.0", "hotfix-", "feature/multi-part-name"} {
		runInDir(t, repoDir, "branch", base, "main")
		want := strings.TrimSpace(runInDir(t, repoDir, "rev-parse", base))
		taskID := "task-" + strings.NewReplacer("/", "-", ".", "-").Replace(base)

		wt, err := mgr.Create(ctx, taskID, base)
		if err != nil {
			t.Fatalf("base branch %q must be accepted: %v", base, err)
		}
		if got := strings.TrimSpace(runInDir(t, wt.Path, "rev-parse", "HEAD")); got != want {
			t.Fatalf("worktree for %q started at %s, want %s", base, got, want)
		}
		if wt.BaseBranch != base {
			t.Fatalf("worktree records base %q, want %q", wt.BaseBranch, base)
		}
	}
}

// TestWorktree_Boundary_RelativeRootDir pins BUG-239: a relative root was applied twice,
// once as git's working directory and again inside the path argument, so the returned Path
// did not name the worktree git created.
func TestWorktree_Boundary_RelativeRootDir(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	t.Chdir(filepath.Dir(repoDir))
	mgr := NewManager(filepath.Base(repoDir))
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-relative", "main")
	if err != nil {
		t.Fatalf("create under a relative root: %v", err)
	}
	if !filepath.IsAbs(wt.Path) {
		t.Fatalf("returned Path must be absolute, got %q", wt.Path)
	}
	gotPath, err := canonicalPath(wt.Path)
	if err != nil {
		t.Fatalf("returned Path %q does not exist: %v", wt.Path, err)
	}
	list, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	registered := false
	for i := 0; i < len(list); i++ {
		if listed, err := canonicalPath(list[i].Path); err == nil && listed == gotPath {
			registered = list[i].Branch == wt.Branch
		}
	}
	if !registered {
		t.Fatalf("returned Path %q is not where git registered %s: %+v", wt.Path, wt.Branch, list)
	}
	if err := mgr.Remove(ctx, wt.TaskID, true); err != nil {
		t.Fatalf("forced removal under a relative root: %v", err)
	}
}

// TestWorktree_Negative_ForceRemovalOfVanishedWorktreeStillDeletesBranch pins BUG-507: when
// 'git worktree remove' itself fails, the managed branch must still be deleted. The fixture
// deletes the directory and prunes the administrative entry, which is how an out-of-band
// cleanup leaves a branch that no worktree owns.
func TestWorktree_Negative_ForceRemovalOfVanishedWorktreeStillDeletesBranch(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-vanished", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatalf("delete worktree directory: %v", err)
	}
	runInDir(t, repoDir, "worktree", "prune")

	err = mgr.Remove(ctx, wt.TaskID, true)
	if err == nil || !strings.Contains(err.Error(), "failed removing worktree") {
		t.Fatalf("the failed worktree removal must be reported, got %v", err)
	}
	if exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "--quiet", wt.Branch).Run() == nil {
		t.Fatalf("branch %s must be deleted even though worktree removal failed", wt.Branch)
	}

	// Removal converges: a second forced removal finds nothing to delete and says so.
	if err := mgr.Remove(ctx, wt.TaskID, true); err == nil {
		t.Fatal("removing an absent worktree and branch must report both failures")
	}
}

// A branch still checked out in a registered worktree is never deleted by the branch step:
// git refuses it, so a failed worktree removal cannot cost the worktree its branch.
func TestWorktree_Boundary_ForceRemovalKeepsBranchOfSurvivingWorktree(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, "task-survivor", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Give Remove a different path than git registered: the managed path is not a worktree
	// anymore, while the branch lives on in the moved one.
	moved := filepath.Join(t.TempDir(), "moved")
	runInDir(t, repoDir, "worktree", "move", wt.Path, moved)

	if err := mgr.Remove(ctx, wt.TaskID, true); err == nil {
		t.Fatal("removing a worktree that is not at the managed path must fail")
	}
	if got := strings.TrimSpace(runInDir(t, moved, "branch", "--show-current")); got != wt.Branch {
		t.Fatalf("the surviving worktree lost branch %s, now on %q", wt.Branch, got)
	}
}

// TestWorktree_Positive_PruneSweepsMergedOrphanBranches pins the other half of BUG-507: Prune
// deletes a managed branch no worktree owns once HEAD contains its commits, and keeps a
// branch carrying unpublished commits, which is what safe removal preserves it for.
func TestWorktree_Positive_PruneSweepsMergedOrphanBranches(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	merged, err := mgr.Create(ctx, "task-merged", "main")
	if err != nil {
		t.Fatalf("create merged: %v", err)
	}
	unmerged, err := mgr.Create(ctx, "task-unmerged", "main")
	if err != nil {
		t.Fatalf("create unmerged: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unmerged.Path, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runInDir(t, unmerged.Path, "add", "work.txt")
	runInDir(t, unmerged.Path, "commit", "-m", "unpublished work")
	live, err := mgr.Create(ctx, "task-live", "main")
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	for _, wt := range []*Worktree{merged, unmerged} {
		if err := mgr.Remove(ctx, wt.TaskID, false); err != nil {
			t.Fatalf("safe removal of %s: %v", wt.TaskID, err)
		}
	}

	if err := mgr.Prune(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	exists := func(branch string) bool {
		return exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
	}
	if exists(merged.Branch) {
		t.Errorf("merged orphan branch %s must be swept", merged.Branch)
	}
	if !exists(unmerged.Branch) {
		t.Errorf("branch %s carries unpublished commits and must be kept", unmerged.Branch)
	}
	if !exists(live.Branch) {
		t.Errorf("branch %s is checked out in a registered worktree and must be kept", live.Branch)
	}
	if !exists("main") {
		t.Error("prune must never touch an unmanaged branch")
	}
}

// Prune in a repository with no commit yet has no managed branch to sweep and must not
// fail on the unborn HEAD that a --merged query would reject.
func TestWorktree_Boundary_PruneOnUnbornHead(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	runInDir(t, dir, "init", "-b", "main")

	if err := NewManager(dir).Prune(context.Background()); err != nil {
		t.Fatalf("prune on an unborn HEAD: %v", err)
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

	// Any error would satisfy err != nil; the cancellation itself must be what is reported,
	// so a caller can tell a cancelled operation from a failed one with errors.Is.
	if _, err := mgr.Create(ctx, "task-cancelled", "main"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Create, got %v", err)
	}
	if err := mgr.Remove(ctx, "task-cancelled", false); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Remove, got %v", err)
	}
	if err := mgr.Remove(ctx, "task-cancelled", true); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on forced Remove, got %v", err)
	}
	if _, err := mgr.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on List, got %v", err)
	}
	if err := mgr.Prune(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Prune, got %v", err)
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

	// Exceeded length (129 chars) is decided by validation alone, on every platform.
	tooLongID := strings.Repeat("x", MaxTaskIDLength+1)
	if _, err := mgr.Create(ctx, tooLongID, "main"); !errors.Is(err, ErrTaskIDTooLong) {
		t.Errorf("expected ErrTaskIDTooLong for %d chars, got %v", MaxTaskIDLength+1, err)
	}

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
		// HISS-21: skip with the reason rather than retry with a shorter id. Retrying at 48
		// characters and passing claimed a MaxTaskIDLength boundary this host never exercised.
		t.Skipf("Windows Git PATH_MAX (260 bytes) cannot hold a %d-character task id under %s; "+
			"the MaxTaskIDLength boundary is verified on Linux and macOS: %v", MaxTaskIDLength, repoDir, err)
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
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	mEmpty := NewManager("")
	if mEmpty.RootDir() != cwd {
		t.Errorf("expected the current directory %q for empty rootDir, got %q", cwd, mEmpty.RootDir())
	}

	mSlash := NewManager("/tmp/foo/bar///")
	expectedClean, err := filepath.Abs("/tmp/foo/bar")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if mSlash.RootDir() != expectedClean {
		t.Errorf("expected %q, got %q", expectedClean, mSlash.RootDir())
	}

	mRelative := NewManager(filepath.Join("some", "..", "repo"))
	if want := filepath.Join(cwd, "repo"); mRelative.RootDir() != want {
		t.Errorf("expected relative root resolved to %q, got %q", want, mRelative.RootDir())
	}

	var nilMgr *Manager
	defaultPath := nilMgr.WorktreePath("task-99")
	if !strings.HasSuffix(defaultPath, filepath.Join(".standards", "worktrees", "task-99")) {
		t.Errorf("unexpected fallback path for nil manager: %s", defaultPath)
	}
}
