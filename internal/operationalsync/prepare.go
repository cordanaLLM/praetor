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
		if err := op.backfillManifestOverlay(ctx); err != nil {
			return err
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
		if !overlayPath(path) {
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
	if _, err := op.git.run(ctx, op.opts.Destination, args...); err != nil {
		return err
	}
	return op.writeSurfaces(ctx)
}

// backfillManifestOverlay writes the current .standards.yaml overlay into an up-to-date
// candidate -- one whose merge left nothing pending, because the reviewed source is already an
// ancestor of the reviewed owner commit -- when the checked-out manifest predates an
// overlay-added field (repository.source; #255/#258) that checkOverlay's
// equivalentManifestOverlay let through. A real merge always runs writeOverlay unconditionally
// and so already carries any such field; the up-to-date branch skips that call, so without this
// step a fork whose manifest was overlaid before the field existed would keep failing every
// later plan/prepare (#263) even once checkOverlay stopped refusing it. It writes and stages
// nothing when the checked-out manifest already equals the expected overlay. The funding
// surfaces are rendered the same way: an owner tree that still carries the engine's unrendered
// surfaces passes plan, so the up-to-date candidate receives the rendering here.
func (op *operation) backfillManifestOverlay(ctx context.Context) error {
	if err := op.writeSurfaces(ctx); err != nil {
		return err
	}
	raw, err := util.ReadConfined(op.opts.Destination, ownerPaths[0])
	if err != nil {
		return err
	}
	if equivalent(ownerPaths[0], raw, op.expected[ownerPaths[0]]) {
		return nil
	}
	if err := util.WriteFileConfined(op.opts.Destination, ownerPaths[0], op.expected[ownerPaths[0]], 0o644); err != nil {
		return err
	}
	_, err = op.git.run(ctx, op.opts.Destination, "add", "--", ownerPaths[0])
	return err
}

// writeOverlayFiles writes the expected overlay into a working tree and touches no index; init
// shares it. Each file is replaced through a pinned handle on root (util.WriteFileConfined), so
// a symlinked ancestor such as .paperclip cannot redirect the overlay, and a link at an overlay
// file is refused instead of written through (BUG-826).
func (op *operation) writeOverlayFiles(root string) error {
	for _, path := range ownerPaths {
		if err := util.WriteFileConfined(root, path, op.expected[path], 0o644); err != nil {
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
	if err := op.verifyOverlayFiles(ctx, tree); err != nil {
		return err
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

// verifyOverlayFiles requires the candidate tree to carry exactly the expected identity
// overlay and the rendered funding surfaces.
func (op *operation) verifyOverlayFiles(ctx context.Context, tree string) error {
	for _, path := range ownerPaths {
		raw, err := op.git.blob(ctx, op.opts.Destination, tree, path)
		if err != nil {
			return err
		}
		if !equivalent(path, raw, op.expected[path]) {
			return fmt.Errorf("candidate differs from expected owner overlay: %s", path)
		}
	}
	return op.verifySurfaces(ctx, tree)
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

// gateReceiptRecord is the `status --porcelain=v1 -z` record of the gate's Exit-0 receipt left
// untracked at the checkout root. .gitignore deliberately keeps it visible evidence and the
// normal flow never commits it, so every owner checkout `praetorctl gate run` ran in carries
// it, and refusing it made the operator move it aside by hand before each run (#136).
const gateReceiptRecord = "?? " + util.GateReceiptFile

// withoutGateReceipt returns one `status --porcelain=v1 -z` listing of root without the gate
// receipt's record, and every other record unchanged. Exactly one record is dropped, and only
// while root holds the receipt as a regular file: a link or a directory at that path, the same
// name anywhere else, and a staged or tracked receipt all remain changes. Nothing reads the
// receipt, so tolerating it cannot change what any stage transforms.
func withoutGateReceipt(root, status string) string {
	var kept strings.Builder
	dropped := false
	for _, record := range strings.Split(status, "\x00") {
		if record == "" {
			continue
		}
		if !dropped && record == gateReceiptRecord && regularGateReceipt(root) {
			dropped = true
			continue
		}
		kept.WriteString(record)
		kept.WriteString("\x00")
	}
	return kept.String()
}

func regularGateReceipt(root string) bool {
	info, err := os.Lstat(filepath.Join(root, util.GateReceiptFile))
	return err == nil && info.Mode().IsRegular()
}

func verifyInputsUnchanged(ctx context.Context, op *operation) error {
	head, err := op.git.text(ctx, op.opts.OwnerPath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != op.opts.OwnerSHA {
		return errors.New("owner HEAD differs from the reviewed owner commit")
	}
	status, err := op.git.run(ctx, op.opts.OwnerPath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if withoutGateReceipt(op.opts.OwnerPath, string(status)) != "" {
		return errors.New("owner checkout has uncommitted or untracked changes")
	}
	// Preserve evidence on every failure; only the caller may remove this owned candidate.
	if filepath.Clean(op.opts.Destination) == filepath.Clean(op.opts.OwnerPath) {
		return errors.New("candidate overlaps owner")
	}
	return nil
}
