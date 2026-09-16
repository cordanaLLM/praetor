package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const bugLedgerName = "BUGS.md"
const bugPendingName = "BUGS.md.pending"
const bugLockName = ".bugs.lock"

// Reads reuse the same bounded, no-symlink snapshot contract as ingestion.
func readBugLedger(ctx context.Context, rootPath string) (data []byte, err error) {
	root, absent, err := openBugReadRoot(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("open bug ledger directory: %w", err)
	}
	if absent {
		return []byte(defaultBugsMD()), nil
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if _, err := root.Lstat(bugLedgerName); errors.Is(err, os.ErrNotExist) {
		return []byte(defaultBugsMD()), nil
	} else if err != nil {
		return nil, err
	}
	// Once observed, disappearing or replaced files are errors, never absence.
	return contextopt.ReadRootSnapshot(ctx, root, bugLedgerName)
}

func openBugReadRoot(ctx context.Context, rootPath string) (child *os.Root, absent bool, err error) {
	repo, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, repo.Close()) }()
	before, err := repo.Lstat(WorkingDirName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !before.IsDir() {
		return nil, false, fmt.Errorf("working directory must not be a symlink or special file")
	}
	child, err = repo.OpenRoot(WorkingDirName)
	if err != nil {
		return nil, false, err
	}
	actual, err := child.Stat(".")
	if err != nil || !os.SameFile(before, actual) {
		return nil, false, errors.Join(fmt.Errorf("working directory changed while opening"), err, child.Close())
	}
	return child, false, nil
}

func updateBugLedger(rootPath string, change func(*bugDocument) (string, error)) (err error) {
	if err := InitWorkingDir(rootPath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, err := contextopt.OpenDirectoryIn(ctx, rootPath, WorkingDirName)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	unlock, err := lockBugLedger(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	before, err := contextopt.ReadRootSnapshot(ctx, root, bugLedgerName)
	if err != nil {
		return err
	}
	doc, err := parseBugDocument(string(before))
	if err != nil {
		return err
	}
	updated, err := change(doc)
	if err != nil {
		return err
	}
	if _, err := parseBugDocument(updated); err != nil {
		return fmt.Errorf("refuse invalid ledger update: %w", err)
	}
	return replaceBugLedger(ctx, root, before, []byte(updated))
}

// Stage and sync before replacement. A failed .pending file is retained; it
// blocks another mutation until inspected, rather than silently discarding data.
func replaceBugLedger(ctx context.Context, root *os.Root, before, updated []byte) error {
	info, err := root.Lstat(bugLedgerName)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("bug ledger must be a regular file")
	}
	file, err := root.OpenFile(bugPendingName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("stage bug ledger (inspect any retained %s): %w", bugPendingName, err)
	}
	_, writeErr := file.Write(updated)
	err = errors.Join(writeErr, file.Chmod(info.Mode().Perm()), file.Sync(), file.Close())
	if err != nil {
		return err
	}
	current, err := contextopt.ReadRootSnapshot(ctx, root, bugLedgerName)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, current) {
		return fmt.Errorf("bug ledger changed before replacement; staged data retained")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(bugPendingName, bugLedgerName); err != nil {
		return err
	}
	return contextopt.SyncDirectory(ctx, root)
}
