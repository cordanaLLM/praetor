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
	"github.com/cordanaLLM/praetor/internal/util"
)

// remoteTrackingPrefix is the ref namespace an upstream must live in to count as pushed; a
// local branch configured as upstream proves nothing left the machine.
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
// in_review run must leave a clean working tree and a HEAD already pushed to its
// remote-tracking upstream; an attached receipt must verify against opts.PinnedKey.
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

	// Invariant: Pushing is NOT shipping, but an in_review run must at least have pushed.
	if status == StatusInReview {
		if err := verifyCleanWorktree(ctx, repoPath, opts.DispositionPath); err != nil {
			return err
		}
		if err := verifyPushed(ctx, repoPath); err != nil {
			return err
		}
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

// verifyCleanWorktree fails when the working tree has uncommitted changes other than the
// disposition file itself.
func verifyCleanWorktree(ctx context.Context, repoPath, dispositionPath string) error {
	args := []string{"status", "--porcelain"}
	if exclude, ok := dispositionPathspec(repoPath, dispositionPath); ok {
		args = append(args, "--", ":(top)", exclude)
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
	return ":(exclude,literal)" + filepath.ToSlash(rel), true
}

// verifyPushed fails unless HEAD's branch tracks a remote-tracking upstream that already
// contains every local commit. The check reads only local refs: the tracking ref records the
// last push or fetch, and no network call is made.
func verifyPushed(ctx context.Context, repoPath string) error {
	upstream, err := util.RunGit(ctx, repoPath, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return fmt.Errorf("contract violation: HEAD has no upstream branch; push it with 'git push -u' before disposition: %w", err)
	}
	if !strings.HasPrefix(upstream, remoteTrackingPrefix) {
		return fmt.Errorf("contract violation: upstream %q is not a remote-tracking branch; push the branch before disposition", upstream)
	}
	ahead, err := util.RunGit(ctx, repoPath, "rev-list", "--count", upstream+"..HEAD")
	if err != nil {
		return fmt.Errorf("verify HEAD is pushed to %s: %w", upstream, err)
	}
	if ahead != "0" {
		return fmt.Errorf("contract violation: %s local commit(s) not pushed to %s; push branch before disposition", ahead, upstream)
	}
	return nil
}
