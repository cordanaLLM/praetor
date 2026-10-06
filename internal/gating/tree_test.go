package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// treeGit runs a fixture git command in dir with a fixed identity and no signing.
func treeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, dir, args...); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// commitFile writes rel in dir and commits it.
func commitFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, rel), content)
	treeGit(t, dir, "add", "-f", "--", rel)
	treeGit(t, dir, "commit", "-q", "-m", "add "+rel)
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	head, err := util.RunGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(head)
}

// refusedRun runs a pipeline that could mint a receipt and requires the clean-tree refusal:
// rejected at the precondition, before any stage, with no receipt written.
func refusedRun(t *testing.T, dir, wantInReason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep, err := RunGatedPipeline(ctx, dir, RunOptions{})
	if err != nil {
		t.Fatalf("RunGatedPipeline: %v", err)
	}
	if rep.Status != StatusRejected || rep.WorktreeClean {
		t.Fatalf("status = %s, worktree clean = %v; want a rejected, unclean run", rep.Status, rep.WorktreeClean)
	}
	if len(rep.Stages) != 1 || rep.Stages[0].Name != TreePreconditionStage || !rep.Stages[0].Failed() {
		t.Fatalf("want exactly the failed %q stage, got %+v", TreePreconditionStage, rep.Stages)
	}
	if !strings.Contains(rep.Stages[0].Message, wantInReason) || !strings.Contains(rep.WorktreeProblem, wantInReason) {
		t.Fatalf("reason %q / problem %q does not name %q", rep.Stages[0].Message, rep.WorktreeProblem, wantInReason)
	}
	if rep.ReceiptSignature != "" {
		t.Fatal("a refused run carries a receipt signature")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused run wrote a receipt: %v", statErr)
	}
}

// TestInspectTree_Positive_CommittedTreeIsCleanDespiteItsReceipt: a committed tree is clean at
// HEAD, and an untracked receipt left by an earlier run does not make it dirty.
func TestInspectTree_Positive_CommittedTreeIsCleanDespiteItsReceipt(t *testing.T) {
	dir := newHermeticGitRepo(t)
	commitFile(t, dir, BaselineFile, "{}\n")
	writeFile(t, filepath.Join(dir, ReceiptFileName), "{}\n")

	tree := inspectTree(t.Context(), dir)
	if tree.problem != "" {
		t.Fatalf("a committed tree with an untracked receipt reads as unclean: %s", tree.problem)
	}
	if want := headOf(t, dir); tree.commit != want {
		t.Fatalf("commit = %q, want HEAD %q", tree.commit, want)
	}
}

// TestRunGatedPipeline_Negative_UncleanTreeIsRefusedBeforeAnyStage: every way the working tree
// can differ from HEAD refuses a run that could mint a receipt.
func TestRunGatedPipeline_Negative_UncleanTreeIsRefusedBeforeAnyStage(t *testing.T) {
	t.Run("modified file", func(t *testing.T) {
		dir := newHermeticGitRepo(t)
		writeFile(t, filepath.Join(dir, "README.md"), "edited\n")
		refusedRun(t, dir, " M README.md")
	})
	t.Run("untracked file", func(t *testing.T) {
		dir := newHermeticGitRepo(t)
		if err := os.Mkdir(filepath.Join(dir, "scratch"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "scratch", "notes.txt"), "n\n")
		refusedRun(t, dir, "?? scratch/notes.txt")
	})
	t.Run("untracked file hidden by status.showUntrackedFiles=no", func(t *testing.T) {
		dir := newHermeticGitRepo(t)
		treeGit(t, dir, "config", "status.showUntrackedFiles", "no")
		writeFile(t, filepath.Join(dir, "hidden.go"), "package hidden\n")
		refusedRun(t, dir, "?? hidden.go")
	})
	t.Run("baseline edited behind assume-unchanged", func(t *testing.T) {
		dir := newHermeticGitRepo(t)
		commitFile(t, dir, BaselineFile, "{}\n")
		treeGit(t, dir, "update-index", "--assume-unchanged", BaselineFile)
		writeFile(t, filepath.Join(dir, BaselineFile), `{"total_infractions": 999}`+"\n")
		refusedRun(t, dir, "assume-unchanged "+BaselineFile)
	})
}

// TestRunGatedPipeline_Negative_IgnoredSubtractiveInputIsRefused: git status never lists an
// ignored file, so an ignored debt baseline or gosec configuration is refused on its own check.
func TestRunGatedPipeline_Negative_IgnoredSubtractiveInputIsRefused(t *testing.T) {
	for _, input := range subtractiveInputs(t.TempDir()) {
		t.Run(input, func(t *testing.T) {
			dir := newHermeticGitRepo(t)
			commitFile(t, dir, ".gitignore", input+"\n")
			writeFile(t, filepath.Join(dir, input), "{}\n")
			refusedRun(t, dir, input+" is not tracked")
		})
	}
}

// TestRunGatedPipeline_Negative_GloballyIgnoredSubtractiveInputIsRefused: the status probe
// honours the user's global excludes file, so an input only that file ignores is missing from
// git status as well; it is still refused, because the check asks whether HEAD's index holds
// the input rather than which ignore rules the sealed probe happens to read.
func TestRunGatedPipeline_Negative_GloballyIgnoredSubtractiveInputIsRefused(t *testing.T) {
	for _, input := range subtractiveInputs(t.TempDir()) {
		t.Run(input, func(t *testing.T) {
			dir := newHermeticGitRepo(t)
			withGlobalIgnore(t, input+"\n")
			writeFile(t, filepath.Join(dir, input), `{"total_infractions": 999}`+"\n")
			changes, err := util.GitWorkingTreeChanges(t.Context(), dir, util.GitTreeProbeTimeout)
			if err != nil || len(changes) != 0 {
				t.Fatalf("the global ignore must hide %s from the status probe: changes = %v, err = %v", input, changes, err)
			}
			refusedRun(t, dir, input+" is not tracked")
		})
	}
}

// TestInspectTree_Boundary_TrackedInputAnIgnoreRuleMatchesIsClean: a committed input stays
// trusted when an ignore rule, repository or global, also matches it, because git tracks a
// file in the index whatever its patterns say.
func TestInspectTree_Boundary_TrackedInputAnIgnoreRuleMatchesIsClean(t *testing.T) {
	dir := newHermeticGitRepo(t)
	withGlobalIgnore(t, GosecConfigFile+"\n")
	commitFile(t, dir, ".gitignore", BaselineFile+"\n")
	commitFile(t, dir, BaselineFile, "{}\n")
	commitFile(t, dir, GosecConfigFile, "{}\n")
	if tree := inspectTree(t.Context(), dir); tree.problem != "" {
		t.Fatalf("committed inputs an ignore rule matches read as unclean: %s", tree.problem)
	}
}

// withGlobalIgnore points this test's home directory at a global excludes file holding
// patterns, with no global git config, so the status probe's default lookup finds it.
func withGlobalIgnore(t *testing.T, patterns string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "no-such-gitconfig"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "git"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".config", "git", "ignore"), patterns)
}

// TestRunGatedPipeline_Negative_SymlinkedSubtractiveInputIsRefused: a tracked symlink reads as
// clean and not ignored, yet the stages follow it to content HEAD does not carry -- an ignored
// file or one outside the repository -- so a subtractive input must be a regular file (BUG-788).
func TestRunGatedPipeline_Negative_SymlinkedSubtractiveInputIsRefused(t *testing.T) {
	for _, input := range subtractiveInputs(t.TempDir()) {
		t.Run(input+" to an ignored target", func(t *testing.T) {
			dir := newHermeticGitRepo(t)
			commitFile(t, dir, ".gitignore", "local/\n")
			if err := os.Mkdir(filepath.Join(dir, "local"), 0o750); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "local", "relaxed.json"), `{"total_infractions": 999}`+"\n")
			commitSymlink(t, dir, input, filepath.Join("local", "relaxed.json"))
			if tree := inspectTree(t.Context(), dir); !strings.Contains(tree.problem, input+" is a symbolic link") {
				t.Fatalf("a tracked symlink to an ignored target must be refused, problem = %q", tree.problem)
			}
			refusedRun(t, dir, input+" is a symbolic link")
		})
		t.Run(input+" outside the repository", func(t *testing.T) {
			dir := newHermeticGitRepo(t)
			outside := filepath.Join(t.TempDir(), "relaxed.json")
			writeFile(t, outside, "{}\n")
			commitSymlink(t, dir, input, outside)
			refusedRun(t, dir, input+" is a symbolic link")
		})
	}
}

// TestPresentSubtractiveInputs_Boundary_OnlyRegularFilesCount: an absent input is skipped, a
// regular file is listed, and anything else at the path -- here a directory -- is refused.
func TestPresentSubtractiveInputs_Boundary_OnlyRegularFilesCount(t *testing.T) {
	dir := t.TempDir()
	if present, problem := presentSubtractiveInputs(dir); len(present) != 0 || problem != "" {
		t.Fatalf("no inputs: present = %v, problem = %q", present, problem)
	}
	writeFile(t, filepath.Join(dir, BaselineFile), "{}\n")
	if present, problem := presentSubtractiveInputs(dir); problem != "" || len(present) != 1 || present[0] != BaselineFile {
		t.Fatalf("a regular baseline: present = %v, problem = %q", present, problem)
	}
	if err := os.Mkdir(filepath.Join(dir, GosecConfigFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, problem := presentSubtractiveInputs(dir); !strings.Contains(problem, GosecConfigFile+" is not a regular file") {
		t.Fatalf("a directory at %s must be refused, problem = %q", GosecConfigFile, problem)
	}
}

// commitSymlink commits rel in dir as a symbolic link to target, skipping where the platform or
// account cannot create one (Windows without developer mode).
func commitSymlink(t *testing.T, dir, rel, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, rel)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	treeGit(t, dir, "add", "-f", "--", rel)
	treeGit(t, dir, "commit", "-q", "-m", "link "+rel)
}

// TestRunGatedPipeline_Boundary_DryRunReportsButDoesNotRefuse: a dry run mints nothing, so an
// unclean tree is reported and the stages still run.
func TestRunGatedPipeline_Boundary_DryRunReportsButDoesNotRefuse(t *testing.T) {
	dir := newHermeticGitRepo(t)
	writeFile(t, filepath.Join(dir, "scratch.txt"), "s\n")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep, err := RunGatedPipeline(ctx, dir, RunOptions{DryRun: true})
	if err != nil {
		t.Fatalf("RunGatedPipeline: %v", err)
	}
	if rep.WorktreeClean || !strings.Contains(rep.WorktreeProblem, "?? scratch.txt") {
		t.Fatalf("a dry run must still report the unclean tree: clean=%v problem=%q", rep.WorktreeClean, rep.WorktreeProblem)
	}
	if len(rep.Stages) == 0 || rep.Stages[0].Name == TreePreconditionStage {
		t.Fatalf("a dry run must not be refused at the precondition: %+v", rep.Stages)
	}
}

// TestRunReceiptStage_Negative_TreeMustStillMatchTheStartingHead: the receipt stage re-reads
// the tree and refuses to sign when it was unclean, changed, or moved to another commit.
func TestRunReceiptStage_Negative_TreeMustStillMatchTheStartingHead(t *testing.T) {
	const start = "0123456789abcdef0123456789abcdef01234567"
	cases := map[string]struct {
		clean bool
		now   treeState
		want  string
	}{
		"recorded unclean": {clean: false, now: treeState{commit: start}, want: "scratch.txt"},
		"changed meanwhile": {clean: true, now: treeState{commit: start, problem: "1 changed path(s)"},
			want: "changed while the gate ran"},
		"HEAD moved": {clean: true, now: treeState{commit: "fedcba9876543210fedcba9876543210fedcba98"},
			want: "HEAD moved from " + start},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repoDir := t.TempDir()
			cfg, _ := newTestConfig(t, repoDir, false)
			cfg.rep.CommitSHA, cfg.rep.WorktreeClean = start, tc.clean
			cfg.rep.WorktreeProblem = "1 changed path(s) differ from HEAD: ?? scratch.txt"
			cfg.inspectTree = func(context.Context, string) treeState { return tc.now }

			_, err := runReceiptStage(t.Context(), cfg)
			if !errors.Is(err, ErrUncleanTree) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want ErrUncleanTree naming %q, got %v", tc.want, err)
			}
			if _, statErr := os.Stat(filepath.Join(repoDir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused receipt stage wrote a receipt: %v", statErr)
			}
		})
	}
}
