package paperclip

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// remoteTrackingPrefix is the ref namespace that records what a remote holds. Only a ref here
// can show HEAD left the machine; a local branch containing HEAD proves nothing.
const remoteTrackingPrefix = "refs/remotes/"

// VerifyOptions carries the trust inputs VerifyRun cannot derive from the disposition itself.
type VerifyOptions struct {
	// PinnedKey is the manifest-pinned Ed25519 receipt key (lockdown.PinnedPublicKey). It is
	// required when the disposition carries a receipt and ignored otherwise.
	PinnedKey ed25519.PublicKey
	// DispositionPath is the file the disposition was read from. When it lies under repoPath,
	// that file alone is left out of the clean-worktree check: the documented workflow writes
	// the record after the branch is pushed, so it is untracked by design.
	DispositionPath string
}

// ReadDisposition loads and parses a disposition JSON file.
func ReadDisposition(path string) (*Disposition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return ReadDispositionContext(ctx, path)
}

func ReadDispositionContext(ctx context.Context, path string) (*Disposition, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read disposition file: %w", err)
	}

	var d Disposition
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse disposition json: %w", err)
	}
	return &d, nil
}

// VerifyRun asserts that a Paperclip agent run satisfies Rule 0 and contract invariants. An
// in_review run must leave a clean working tree (its disposition file and the gate receipt
// aside) and a HEAD that a remote-tracking ref already contains. An attached receipt must
// verify against opts.PinnedKey and attest the commit checked out in repoPath, so a receipt
// signed for an earlier commit cannot be replayed.
func VerifyRun(ctx context.Context, repoPath string, d *Disposition, opts VerifyOptions) error {
	if ctx == nil {
		return fmt.Errorf("verify: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify cancelled: %w", err)
	}

	status, err := d.validate(ctx, opts.PinnedKey)
	if err != nil {
		return fmt.Errorf("disposition invalid: %w", err)
	}
	if err := verifyRepositoryState(ctx, repoPath, d, status, opts); err != nil {
		return err
	}

	// A file's presence cannot establish a valid configured harness.
	harnessPath := filepath.Join(repoPath, ".paperclip", "harness.json")
	if _, err := LoadHarnessContext(ctx, harnessPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("missing .paperclip/harness.json in %s; run 'praetorctl paperclip harness'", repoPath)
		}
		return fmt.Errorf("verify harness configuration: %w", err)
	}

	return nil
}

// verifyRepositoryState checks the claims a validated disposition makes about repoPath: an
// attached receipt attests the checked-out commit, and an in_review run left a clean, pushed
// tree.
func verifyRepositoryState(ctx context.Context, repoPath string, d *Disposition, status DispositionStatus, opts VerifyOptions) error {
	if d.Receipt != nil {
		if err := lockdown.VerifyReceiptCommit(ctx, repoPath, d.Receipt.CommitSHA); err != nil {
			return fmt.Errorf("disposition receipt: %w", err)
		}
	}
	// Invariant: Pushing is NOT shipping, but an in_review run must at least have pushed.
	if status != StatusInReview {
		return nil
	}
	if err := verifyCleanWorktree(ctx, repoPath, opts.DispositionPath); err != nil {
		return err
	}
	return verifyPushed(ctx, repoPath)
}

// verifyCleanWorktree fails when the working tree has uncommitted changes other than the
// disposition file itself and the gate's Exit-0 receipt at repoPath/gating.ReceiptFileName.
// `praetorctl gate run` writes that receipt for the checked-out commit, so the documented order
// (commit, push, mint the receipt, write the disposition) leaves it untracked, or modified where
// a repository tracks it. It is gate output, not unpushed work, and committing it would move HEAD
// off the commit the receipt attests.
func verifyCleanWorktree(ctx context.Context, repoPath, dispositionPath string) error {
	args := []string{"status", "--porcelain", "--", ":(top)", literalExclude(gating.ReceiptFileName)}
	if exclude, ok := dispositionPathspec(repoPath, dispositionPath); ok {
		args = append(args, exclude)
	}
	uncommitted, err := util.RunGit(ctx, repoPath, args...)
	if err != nil {
		return fmt.Errorf("verify working tree git status: %w", err)
	}
	if uncommitted != "" {
		return fmt.Errorf("contract violation: uncommitted changes exist in working tree; commit and push branch before disposition")
	}
	return nil
}

// dispositionPathspec returns a literal exclude pathspec for dispositionPath, relative to
// repoPath (git's working directory), when the file lies under repoPath.
func dispositionPathspec(repoPath, dispositionPath string) (string, bool) {
	if strings.TrimSpace(dispositionPath) == "" {
		return "", false
	}
	root, err := filepath.Abs(repoPath)
	if err != nil {
		return "", false
	}
	file, err := filepath.Abs(dispositionPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return literalExclude(filepath.ToSlash(rel)), true
}

// literalExclude returns a pathspec that excludes rel, a slash-separated path relative to git's
// working directory, matched literally rather than as a glob.
func literalExclude(rel string) string {
	return ":(exclude,literal)" + rel
}

// verifyPushed fails unless a remote-tracking ref contains HEAD. It reads only local refs: a
// remote-tracking ref records the last push to or fetch from its remote, and no network call is
// made. Any remote-tracking ref counts, so the review branch the harness push protocol pushes
// and a forge review ref fetched into refs/remotes/ both satisfy it, on a branch or detached.
func verifyPushed(ctx context.Context, repoPath string) error {
	containing, err := util.RunGit(ctx, repoPath, "for-each-ref", "--count=1", "--contains=HEAD",
		"--format=%(refname)", remoteTrackingPrefix)
	if err != nil {
		return fmt.Errorf("verify HEAD is pushed: %w", err)
	}
	if containing == "" {
		return fmt.Errorf("contract violation: HEAD is not pushed: no ref under %s contains it; "+
			"run the push protocol in .paperclip/rules.md before disposition", remoteTrackingPrefix)
	}
	return nil
}
