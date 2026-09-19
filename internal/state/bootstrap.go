package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// InitWorkingDirIfAbsentContext initializes a private ledger that does not yet
// exist. Initialization is keyed on the ledger files rather than on the
// directory alone: another module (milestone, forge, docdistill, dedupe,
// hindsight) routinely creates .workingdir first, and keying on the directory
// left that ledger empty and every later state command failing. A directory
// that already holds ledger files is left exactly as it stands, partial ones
// included, as is an existing non-directory path; created reports whether this
// call made the directory.
//
// The caller must run AuditWorkingDir afterward; this function does not certify
// state. Failed initialization leaves the partial directory for recovery.
func InitWorkingDirIfAbsentContext(ctx context.Context, rootPath string) (created bool, err error) {
	if ctx == nil {
		return false, errors.New("state initialization requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	project, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return false, fmt.Errorf("open project for state bootstrap: %w", err)
	}
	defer func() { err = errors.Join(err, project.Close()) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	created, seedable, err := claimWorkingDirectory(project)
	if err != nil || !seedable {
		return created, err
	}
	return created, seedLedgerlessWorkingDir(ctx, project, created)
}

// seedLedgerlessWorkingDir writes the default ledger files, but only into a
// directory this call created or one that holds no ledger file at all. A
// directory holding some of the five is a partial ledger: a file was removed or
// corrupted, and repairing it here would hide that from AuditWorkingDir and
// from SyncState, which must both fail closed on it.
func seedLedgerlessWorkingDir(ctx context.Context, project *os.Root, created bool) (err error) {
	working, err := openWorkingDirectory(project)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	if !created {
		ledgerless, checkErr := workingDirLedgerless(working)
		if checkErr != nil || !ledgerless {
			return checkErr
		}
	}
	return initializeWorkingFiles(ctx, working)
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
