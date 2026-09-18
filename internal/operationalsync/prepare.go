package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

func (op *operation) prepare(ctx context.Context, report *Report) error {
	o := op.opts
	if err := op.cloneCandidate(ctx); err != nil {
		return err
	}
	_, mergeErr := op.git.run(ctx, o.Destination, "-c", "user.name=Praetor local preparation", "-c", "user.email=praetor-prepare@localhost", "merge", "--no-ff", "--no-commit", o.SourceSHA)
	if mergeErr != nil {
		if err := op.allowOwnerConflicts(ctx); err != nil {
			return errors.Join(mergeErr, err)
		}
	}
	mergeHead, headErr := op.git.text(ctx, o.Destination, "rev-parse", "--verify", "-q", "MERGE_HEAD")
	if headErr != nil {
		if err := op.git.ancestor(ctx, o.Destination, o.SourceSHA, o.OwnerSHA); err != nil {
			return errors.New("candidate lost ordinary merge state")
		}
		report.UpToDate = true
		return op.verifyCandidate(ctx)
	}
	if mergeHead != o.SourceSHA {
		return errors.New("candidate merge parent differs from reviewed source")
	}
	if err := op.writeOverlay(ctx); err != nil {
		return err
	}
	if err := op.verifyCandidate(ctx); err != nil {
		return err
	}
	report.MergePending = true
	return nil
}

func (op *operation) cloneCandidate(ctx context.Context) error {
	o := op.opts
	// New clones do not copy input hooks or config. Never activate repository scripts.
	if err := os.Mkdir(o.Destination, 0o700); err != nil {
		return err
	}
	if _, err := op.git.run(ctx, "", "clone", "--template=", "--no-hardlinks", "--no-checkout", "--", o.OwnerPath, o.Destination); err != nil {
		return err
	}
	if _, err := op.git.run(ctx, o.Destination, "fetch", "--no-tags", "--no-recurse-submodules", "--", o.SourcePath, o.SourceSHA); err != nil {
		return err
	}
	if err := op.configureCandidate(ctx); err != nil {
		return err
	}
	if _, err := op.git.run(ctx, o.Destination, "checkout", "-b", "operational-sync-candidate", o.OwnerSHA); err != nil {
		return err
	}
	return nil
}

func (op *operation) configureCandidate(ctx context.Context) error {
	for key, value := range map[string]string{"remote.origin.url": "https://github.com/" + op.origin + ".git",
		"remote.upstream.url": "https://github.com/" + op.upstream + ".git"} {
		if _, err := op.git.run(ctx, op.opts.Destination, "config", "--local", key, value); err != nil {
			return err
		}
	}
	return nil
}

func (op *operation) allowOwnerConflicts(ctx context.Context) error {
	out, err := op.git.text(ctx, op.opts.Destination, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--diff-filter=U")
	if err != nil {
		return err
	}
	if out == "" {
		return errors.New("merge failed without a resolvable owner configuration conflict")
	}
	for _, path := range strings.Split(out, "\n") {
		if !allowedPath(path) {
			return fmt.Errorf("non-owner merge conflict: %s", path)
		}
	}
	return nil
}

func (op *operation) writeOverlay(ctx context.Context) error {
	if err := op.writeOverlayFiles(op.opts.Destination); err != nil {
		return err
	}
	args := append([]string{"add", "--"}, ownerPaths...)
	_, err := op.git.run(ctx, op.opts.Destination, args...)
	return err
}

// writeOverlayFiles writes the expected overlay into a working tree and touches no index; init shares it.
func (op *operation) writeOverlayFiles(root string) error {
	for _, path := range ownerPaths {
		target, err := util.ConfinePath(root, path)
		if err != nil {
			return err
		}
		if err := util.WriteFileSecure(target, op.expected[path], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (op *operation) verifyCandidate(ctx context.Context) error {
	dir := op.opts.Destination
	unmerged, err := op.git.text(ctx, dir, "ls-files", "--unmerged")
	if err != nil {
		return err
	}
	if unmerged != "" {
		return errors.New("candidate retains unresolved conflicts")
	}
	tree, err := op.git.text(ctx, dir, "write-tree")
	if err != nil {
		return err
	}
	ownerOnly, err := op.checkPaths(ctx, dir, op.opts.SourceSHA, tree)
	if err != nil {
		return err
	}
	if !slices.Equal(ownerOnly, op.ownerOnly) {
		return errors.New("candidate owner-only paths differ from the reviewed owner tree")
	}
	for _, path := range ownerPaths {
		raw, err := op.git.blob(ctx, dir, tree, path)
		if err != nil {
			return err
		}
		if !equivalent(path, raw, op.expected[path]) {
			return fmt.Errorf("candidate differs from expected owner overlay: %s", path)
		}
	}
	head, err := op.git.text(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != op.opts.OwnerSHA {
		return errors.New("candidate changed reviewed owner ancestry")
	}
	return op.verifyWorktree(ctx)
}

func (op *operation) verifyWorktree(ctx context.Context) error {
	dir := op.opts.Destination
	if _, err := op.git.run(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--quiet", "--"); err != nil {
		return fmt.Errorf("candidate has unstaged drift: %w", err)
	}
	untracked, err := op.git.text(ctx, dir, "ls-files", "--others", "-z")
	if err != nil {
		return err
	}
	if untracked != "" {
		return errors.New("candidate has untracked files")
	}
	return verifyInputsUnchanged(ctx, op)
}

func verifyInputsUnchanged(ctx context.Context, op *operation) error {
	head, err := op.git.text(ctx, op.opts.OwnerPath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != op.opts.OwnerSHA {
		return errors.New("owner HEAD differs from the reviewed owner commit")
	}
	status, err := op.git.text(ctx, op.opts.OwnerPath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("owner checkout has uncommitted or untracked changes")
	}
	// Preserve evidence on every failure; only the caller may remove this owned candidate.
	if filepath.Clean(op.opts.Destination) == filepath.Clean(op.opts.OwnerPath) {
		return errors.New("candidate overlaps owner")
	}
	return nil
}
