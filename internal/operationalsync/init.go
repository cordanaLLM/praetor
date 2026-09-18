package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const initScope = "Owner configuration overlay written into the working tree of a clean checkout of the public source; nothing staged or committed; no tests, publication or bot activation"

func validateInitOptions(opts *Options) error {
	if opts.SourcePath != "" || opts.OwnerSHA != "" || opts.BaseSHA != "" || opts.SourceSHA != "" || opts.Destination != "" {
		return errors.New("init accepts only the owner checkout path and the owner; it reads the checked-out HEAD")
	}
	if !identityPart.MatchString(opts.Owner) {
		return errors.New("init requires a valid operational repository owner")
	}
	return normalizeCheckoutPath(&opts.OwnerPath)
}

// runInit applies the first owner overlay with the same overlay() that plan and prepare verify
// against, so the first fork commit is engine output. It stages nothing and commits nothing.
func runInit(ctx context.Context, g *gitRunner, opts Options) (*Report, error) {
	op := operation{git: g, opts: opts}
	if err := op.validateInit(ctx); err != nil {
		return nil, err
	}
	report := &Report{Version: 1, Stage: "init", Status: "initializing", Options: op.opts, Owner: op.origin, Source: op.upstream,
		ChangedPaths: append([]string(nil), ownerPaths...), OwnerOnlyPaths: []string{}, Scope: initScope}
	if err := op.applyInit(ctx); err != nil {
		report.Status, report.Error = "failed", err.Error()
		return report, err
	}
	report.Status = "initialized"
	return report, nil
}

// validateInit binds the run to the checked-out HEAD and derives the overlay without writing.
func (op *operation) validateInit(ctx context.Context) error {
	if err := op.checkCheckoutRoots(ctx); err != nil {
		return err
	}
	if err := op.checkInputConfiguration(ctx); err != nil {
		return err
	}
	head, err := op.git.text(ctx, op.opts.OwnerPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	op.opts.OwnerSHA = head
	if err := verifyInputsUnchanged(ctx, op); err != nil {
		return err
	}
	files, err := op.readFiles(ctx, op.opts.OwnerPath, head)
	if err != nil {
		return err
	}
	_, source, err := manifest(files[ownerPaths[0]])
	if err != nil {
		return err
	}
	op.owner = identity{Owner: op.opts.Owner, Name: source.Name, Visibility: "private"}
	if err := op.checkManifestIdentity(ctx, source); err != nil {
		return fmt.Errorf("init requires a HEAD manifest that still carries the public source identity: %w", err)
	}
	if err := op.checkOwnerRemotes(ctx, source); err != nil {
		return err
	}
	op.expected, err = overlay(files, op.owner)
	return err
}

// applyInit writes the four overlay files and proves nothing else moved: same HEAD, empty index
// difference, and a working tree that differs from HEAD in exactly those four paths.
func (op *operation) applyInit(ctx context.Context) error {
	dir := op.opts.OwnerPath
	if err := op.writeOverlayFiles(dir); err != nil {
		return err
	}
	head, err := op.git.text(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != op.opts.OwnerSHA {
		return errors.New("owner HEAD changed during init")
	}
	out, err := op.git.run(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	return checkInitStatus(string(out))
}

// checkInitStatus reads `status --porcelain=v1 -z`: every record must be an unstaged modification
// (" M") of one overlay path, and every overlay path must appear once.
func checkInitStatus(status string) error {
	seen := make(map[string]bool, len(ownerPaths))
	for _, record := range strings.Split(status, "\x00") {
		if record == "" {
			continue
		}
		if len(record) < 4 || record[:3] != " M " || !allowedPath(record[3:]) || seen[record[3:]] {
			return fmt.Errorf("init left an unexpected working tree change: %q", record)
		}
		seen[record[3:]] = true
	}
	if len(seen) != len(ownerPaths) {
		return fmt.Errorf("init changed %d of %d owner overlay files", len(seen), len(ownerPaths))
	}
	return nil
}
