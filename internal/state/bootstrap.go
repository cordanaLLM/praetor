package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// BootstrapOutcome names what a bootstrap call did to .workingdir. Three of the
// five outcomes write nothing, so a caller that reports one fixed sentence for
// every non-creating return tells the operator a ledger was repaired when none
// was. The zero value is BootstrapUnknown: the call did not complete and nothing
// may be claimed about the directory.
type BootstrapOutcome string

const (
	// BootstrapUnknown is the zero value; it accompanies a non-nil error.
	BootstrapUnknown BootstrapOutcome = ""
	// BootstrapCreated: this call made .workingdir and wrote the ledger into it.
	BootstrapCreated BootstrapOutcome = "created"
	// BootstrapSeeded: .workingdir already existed and held no ledger file at
	// all; this call wrote the ledger into it.
	BootstrapSeeded BootstrapOutcome = "seeded"
	// BootstrapKept: .workingdir already held at least one ledger file, complete
	// or partial. Nothing was written; AuditWorkingDir judges what is there.
	BootstrapKept BootstrapOutcome = "kept"
	// BootstrapUnseedable: .workingdir exists as a symlink or a regular file.
	// Nothing was written and nothing can be until the operator removes it.
	BootstrapUnseedable BootstrapOutcome = "unseedable"
	// BootstrapNestedRepository: .workingdir is a directory holding no ledger
	// file but its own .git, so it is the working tree of another repository.
	// Nothing was written: seeding would dirty that repository, and a clone
	// staged as a gitlink must reach the commit hook's privacy check unchanged.
	// An operator who keeps the ledger in its own repository runs `state init`.
	BootstrapNestedRepository BootstrapOutcome = "nested-repository"
)

// InitWorkingDirIfAbsentContext initializes a private ledger that does not yet
// exist. Initialization is keyed on the ledger files rather than on the
// directory alone: another module (milestone, forge, docdistill, dedupe,
// hindsight) routinely creates .workingdir first, and keying on the directory
// left that ledger empty and every later state command failing. A directory
// that already holds ledger files is left exactly as it stands, partial ones
// included, as are an existing non-directory path and a ledgerless directory
// that is another repository's working tree.
//
// outcome reports which of those five cases this call hit, so the caller can
// state what happened rather than guess. Check err first: a returned outcome
// describes the intent of a call that may still have failed part way.
//
// The caller must run AuditWorkingDir afterward; this function does not certify
// state. Failed initialization leaves the partial directory for recovery.
func InitWorkingDirIfAbsentContext(ctx context.Context, rootPath string) (outcome BootstrapOutcome, err error) {
	if ctx == nil {
		return BootstrapUnknown, errors.New("state initialization requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	project, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return BootstrapUnknown, fmt.Errorf("open project for state bootstrap: %w", err)
	}
	defer func() { err = errors.Join(err, project.Close()) }()
	if err := ctx.Err(); err != nil {
		return BootstrapUnknown, err
	}
	created, seedable, err := claimWorkingDirectory(project)
	switch {
	case err != nil:
		return BootstrapUnknown, err
	case !seedable:
		return BootstrapUnseedable, nil
	}
	return seedLedgerlessWorkingDir(ctx, project, created)
}

// seedLedgerlessWorkingDir writes the default ledger files, but only into a
// directory this call created or one that holds no ledger file at all. A
// directory holding some of the five is a partial ledger: a file was removed or
// corrupted, and repairing it here would hide that from AuditWorkingDir and
// from SyncState, which must both fail closed on it. The returned outcome
// distinguishes the directory it seeded from the ledger it refused to touch.
func seedLedgerlessWorkingDir(ctx context.Context, project *os.Root, created bool) (outcome BootstrapOutcome, err error) {
	working, err := openWorkingDirectory(project)
	if err != nil {
		return BootstrapUnknown, err
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	if created {
		return BootstrapCreated, initializeWorkingFiles(ctx, working)
	}
	outcome, err = existingWorkingDirOutcome(working)
	if err != nil || outcome != BootstrapSeeded {
		return outcome, err
	}
	return BootstrapSeeded, initializeWorkingFiles(ctx, working)
}

// existingWorkingDirOutcome decides what bootstrap may do to a directory it did
// not create: keep one that holds ledger files, refuse one that is another
// repository's working tree, and seed the rest.
func existingWorkingDirOutcome(working *os.Root) (BootstrapOutcome, error) {
	ledgerless, err := workingDirLedgerless(working)
	switch {
	case err != nil:
		return BootstrapUnknown, err
	case !ledgerless:
		return BootstrapKept, nil
	}
	_, err = working.Lstat(".git")
	switch {
	case err == nil:
		return BootstrapNestedRepository, nil
	case !errors.Is(err, os.ErrNotExist):
		return BootstrapUnknown, fmt.Errorf("inspect %s/.git: %w", WorkingDirName, err)
	}
	return BootstrapSeeded, nil
}

// LedgerPresent reports whether rootPath's private working directory holds any
// ledger file, by the same test InitWorkingDirIfAbsentContext seeds on. An absent
// working directory holds none, and so does a path under that name that is a
// symlink or a regular file, which no state command writes through. A command
// that may create the ledger as a side effect probes before and after it runs to
// learn whether it did.
func LedgerPresent(ctx context.Context, rootPath string) (present bool, err error) {
	if ctx == nil {
		return false, errors.New("ledger inspection requires a context")
	}
	project, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return false, fmt.Errorf("open project to inspect its ledger: %w", err)
	}
	defer func() { err = errors.Join(err, project.Close()) }()
	info, err := project.Lstat(WorkingDirName)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect private working directory: %w", err)
	case !info.IsDir():
		return false, nil
	}
	working, err := project.OpenRoot(WorkingDirName)
	if err != nil {
		return false, fmt.Errorf("open private working directory: %w", err)
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	ledgerless, err := workingDirLedgerless(working)
	return err == nil && !ledgerless, err
}

// workingDirLedgerless reports whether the working directory holds none of the
// ledger files an initialized one carries. Any existing path under one of those
// names, regular file or not, counts as present: the audit judges it, not this.
func workingDirLedgerless(working *os.Root) (bool, error) {
	for _, name := range ledgerFileNames() {
		_, err := working.Lstat(name)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect ledger %s: %w", name, err)
		}
	}
	return true, nil
}

// claimWorkingDirectory creates the private working directory when it is
// absent. It reports whether this call created it and whether the path is a
// real directory that may be seeded; an existing symlink or file is neither an
// error nor seedable, because the audit must still see it as it stands.
func claimWorkingDirectory(project *os.Root) (created, seedable bool, err error) {
	mkdirErr := project.Mkdir(WorkingDirName, 0700)
	if mkdirErr == nil {
		return true, true, nil
	}
	if !errors.Is(mkdirErr, os.ErrExist) {
		return false, false, fmt.Errorf("create private working directory: %w", mkdirErr)
	}
	info, statErr := project.Lstat(WorkingDirName)
	if statErr != nil {
		return false, false, fmt.Errorf("inspect private working directory: %w", statErr)
	}
	return false, info.IsDir(), nil
}
