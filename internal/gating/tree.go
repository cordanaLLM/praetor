package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// BaselineFile is the HISS debt baseline the HISS stage ratchets against. It raises the
	// number of violations the stage admits, so the gate trusts it only as HEAD carries it.
	BaselineFile = ".standards-baseline.json"
	// TreePreconditionStage names the stage a run that could mint a receipt records when it
	// refuses a working tree that does not match HEAD.
	TreePreconditionStage = "Clean Tree Precondition"
	// maxReportedChanges bounds how many changed paths a refusal names.
	maxReportedChanges = 5
)

// ErrUncleanTree reports a working tree the gate will not certify: a receipt names a commit,
// and the scan stages read the working tree, so the two must be the same content.
var ErrUncleanTree = errors.New("the gate certifies only a working tree that matches HEAD")

// subtractiveInputs are the files that relax what the gate enforces: the debt baseline raises
// the HISS limit and the gosec configuration selects the scanner's rules. git status does not
// list an ignored file, so each is also checked against the repository's ignore rules.
var subtractiveInputs = [...]string{BaselineFile, GosecConfigFile}

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
	changes, err := util.GitWorkingTreeChanges(ctx, repoDir, ":(exclude)"+ReceiptFileName)
	if err != nil {
		return fmt.Sprintf("the working tree's state could not be read: %v", err)
	}
	if len(changes) > 0 {
		return describeChanges(changes)
	}
	return untrackedInputProblem(ctx, repoDir)
}

// describeChanges summarises changed paths as a count and the first few porcelain records.
func describeChanges(changes []string) string {
	shown := changes[:min(len(changes), maxReportedChanges)]
	summary := fmt.Sprintf("%d changed path(s) differ from HEAD: %s", len(changes), strings.Join(shown, ", "))
	if len(changes) > len(shown) {
		summary += fmt.Sprintf(", and %d more", len(changes)-len(shown))
	}
	return summary
}

// untrackedInputProblem names a subtractive input that exists in the working tree while the
// repository ignores it, so HEAD does not carry the file the stages would read.
func untrackedInputProblem(ctx context.Context, repoDir string) string {
	present := make([]string, 0, len(subtractiveInputs))
	for i := 0; i < len(subtractiveInputs); i++ {
		if _, err := os.Lstat(filepath.Join(repoDir, subtractiveInputs[i])); err == nil {
			present = append(present, subtractiveInputs[i])
		}
	}
	ignored, err := util.GitIgnoredPaths(ctx, repoDir, present, false)
	if err != nil {
		return fmt.Sprintf("whether %s is tracked could not be read: %v", strings.Join(present, " and "), err)
	}
	if len(ignored) > 0 {
		return fmt.Sprintf("%s is ignored and untracked, so HEAD does not carry it; "+
			"the gate reads it to relax its checks and refuses one that is not committed", strings.Join(ignored, " and "))
	}
	return ""
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
