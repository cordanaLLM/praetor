package gating

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// BaselineFile is the HISS debt baseline the HISS stage ratchets against. It raises the
	// number of violations the stage admits, so the gate trusts it only as HEAD carries it.
	BaselineFile = ".standards-baseline.json"
	// TreePreconditionStage names the stage a run that could mint a receipt records when it
	// refuses a working tree that does not match HEAD.
	TreePreconditionStage = "Clean Tree Precondition"
)

// ErrUncleanTree reports a working tree the gate will not certify: a receipt names a commit,
// and the scan stages read the working tree, so the two must be the same content.
var ErrUncleanTree = errors.New("the gate certifies only a working tree that matches HEAD")

// subtractiveInputs returns the files that relax what the gate enforces in repoDir: the debt
// baseline raises the HISS limit, the gosec configuration selects the scanner's rules, and the
// OpenVEX document the manifest names (config.RepositoryGoVEXPath) covers advisories the Go
// vulnerability gate would fail. git status does not list an ignored file, so each must also be
// tracked in the index, and it does not look behind a symbolic link, so each must be a regular
// file. A manifest that does not load names no document here; the security stage fails on it.
func subtractiveInputs(repoDir string) []string {
	inputs := []string{BaselineFile, GosecConfigFile}
	if vex, err := config.RepositoryGoVEXPath(repoDir); err == nil {
		inputs = append(inputs, vex)
	}
	return inputs
}

// treeState is what one inspection saw of the tree the scan stages read.
type treeState struct {
	commit  string
	problem string // empty when the working tree matches commit
}

// inspectTree reads HEAD and whether the working tree matches it. The receipt file itself is
// excluded: the gate rewrites it, and a checkout that keeps it untracked is otherwise clean.
func inspectTree(ctx context.Context, repoDir string) treeState {
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	return treeState{commit: getGitCommitSHA(gitCtx, repoDir), problem: treeProblem(gitCtx, repoDir)}
}

// treeProblem names why the working tree does not match HEAD, or returns "" when it does.
func treeProblem(ctx context.Context, repoDir string) string {
	changes, err := util.GitWorkingTreeChanges(ctx, repoDir, GitQueryTimeout, util.GitLiteralExclude(ReceiptFileName))
	if err != nil {
		return fmt.Sprintf("the working tree's state could not be read: %v", err)
	}
	if len(changes) > 0 {
		return util.DescribeWorkingTreeChanges(changes)
	}
	return untrackedInputProblem(ctx, repoDir)
}

// untrackedInputProblem names a subtractive input the stages would read from somewhere other
// than HEAD: one that is not a regular file, or one the index does not hold. It runs after the
// status probe found no change, so an untracked input here is one an ignore rule hides. The
// check asks the index rather than the ignore rules, so it cannot drift from whichever
// excludes files the status probe honours -- the repository's, .git/info/exclude or the
// user's global one (BUG-788).
func untrackedInputProblem(ctx context.Context, repoDir string) string {
	present, problem := presentSubtractiveInputs(repoDir)
	if problem != "" {
		return problem
	}
	untracked, err := util.GitUntrackedPaths(ctx, repoDir, present)
	if err != nil {
		return fmt.Sprintf("whether %s is tracked could not be read: %v", strings.Join(present, " and "), err)
	}
	if len(untracked) > 0 {
		return fmt.Sprintf("%s is not tracked, so HEAD does not carry it, and an ignore rule hides it from git status; "+
			"the gate reads it to relax its checks and refuses one that is not committed", strings.Join(untracked, " and "))
	}
	return ""
}

// presentSubtractiveInputs lists the subtractive inputs that exist in repoDir, or names why one
// cannot be trusted. Each must be a regular file: git status compares a tracked symlink by its
// target path, never by the content behind it, while the stages follow the link, so a clean,
// unignored link could hand them an ignored file or one outside the repository. The link is
// refused rather than resolved, whatever it points at.
func presentSubtractiveInputs(repoDir string) ([]string, string) {
	inputs := subtractiveInputs(repoDir)
	present := make([]string, 0, len(inputs))
	for i := 0; i < len(inputs); i++ {
		name := inputs[i]
		info, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(name)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Sprintf("%s could not be inspected: %v", name, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Sprintf("%s is a symbolic link; the gate reads it to relax its checks and "+
				"trusts only a regular file whose content HEAD carries", name)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Sprintf("%s is not a regular file (mode %s); the gate reads it to relax its checks and "+
				"trusts only a regular file whose content HEAD carries", name, info.Mode().Type())
		}
		present = append(present, name)
	}
	return present, ""
}

// requireCleanTree refuses a run that could mint a receipt when the tree does not match HEAD,
// recording the refusal as a failed precondition stage so every report shows the reason. A
// dry run mints nothing and passes; its report still carries WorktreeProblem.
func requireCleanTree(cfg *stageConfig) error {
	if cfg.dryRun || cfg.rep.WorktreeClean {
		return nil
	}
	err := fmt.Errorf("%w: %s; commit, stash or remove the changes, or preview with --dry-run",
		ErrUncleanTree, cfg.rep.WorktreeProblem)
	cfg.rep.Stages = append(cfg.rep.Stages, StageResult{Name: TreePreconditionStage, Status: StageFailed, Message: err.Error()})
	return err
}

// confirmTreeUnchanged re-reads the tree just before a receipt is signed. The stages run for
// minutes, and the race stage tests whatever HEAD is when it starts, so a commit or an edit
// made meanwhile would otherwise be certified under the commit the run started on.
func confirmTreeUnchanged(ctx context.Context, cfg *stageConfig) error {
	if !cfg.rep.WorktreeClean {
		return fmt.Errorf("%w: %s", ErrUncleanTree, cfg.rep.WorktreeProblem)
	}
	now := cfg.inspectTree(ctx, cfg.repoDir)
	if now.problem != "" {
		return fmt.Errorf("%w: the working tree changed while the gate ran: %s", ErrUncleanTree, now.problem)
	}
	if now.commit != cfg.rep.CommitSHA {
		return fmt.Errorf("%w: HEAD moved from %s to %s while the gate ran", ErrUncleanTree, cfg.rep.CommitSHA, now.commit)
	}
	return nil
}
