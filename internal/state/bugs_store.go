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

// bugStoreFile names one persisted ledger file and its staging name.
type bugStoreFile struct{ name, pending string }

var (
	ledgerStoreFile  = bugStoreFile{bugLedgerName, bugPendingName}
	sidecarStoreFile = bugStoreFile{bugMetaName, bugMetaPendingName}
)

// bugFiles is one read of BUGS.md and its optional metadata sidecar.
type bugFiles struct {
	ledger, meta             []byte
	ledgerExists, metaExists bool
}

// Reads reuse the same bounded, no-symlink snapshot contract as ingestion.
func readBugFiles(ctx context.Context, rootPath string) (files bugFiles, err error) {
	root, absent, err := openBugReadRoot(ctx, rootPath)
	if err != nil {
		return files, fmt.Errorf("open bug ledger directory: %w", err)
	}
	if absent {
		return bugFiles{ledger: []byte(defaultBugsMD())}, nil
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return readBugFilesIn(ctx, root)
}

// readBugFilesIn reads both files from a pinned working directory. A missing
// ledger reads as the empty default; a missing sidecar as no metadata.
func readBugFilesIn(ctx context.Context, root *os.Root) (files bugFiles, err error) {
	files.ledger, files.ledgerExists, err = contextopt.ObserveRootSnapshot(ctx, root, bugLedgerName)
	if err != nil {
		return files, err
	}
	if !files.ledgerExists {
		files.ledger = []byte(defaultBugsMD())
	}
	files.meta, files.metaExists, err = contextopt.ObserveRootSnapshot(ctx, root, bugMetaName)
	return files, err
}

// parseBugFiles validates the ledger against its sidecar. The document always
// carries a non-nil index for writers to update.
func parseBugFiles(files bugFiles) (*bugDocument, error) {
	index := bugMetaIndex{}
	if files.metaExists {
		decoded, err := decodeBugMeta(files.meta)
		if err != nil {
			return nil, err
		}
		index = decoded
	}
	return parseBugDocument(string(files.ledger), index)
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

func updateBugLedger(rootPath string, change func(*bugDocument) (string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return updateBugLedgerContext(ctx, rootPath, change)
}

// updateBugLedgerContext runs change under the ledger lock. change returns the
// new ledger text and may update doc.meta; both are validated together before
// anything is written.
func updateBugLedgerContext(ctx context.Context, rootPath string, change func(*bugDocument) (string, error)) (err error) {
	if err := InitWorkingDirContext(ctx, rootPath); err != nil {
		return err
	}
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
	files, err := readBugFilesIn(ctx, root)
	if err != nil {
		return err
	}
	if !files.ledgerExists {
		return fmt.Errorf("bug ledger %s is missing", bugLedgerName)
	}
	doc, err := parseBugFiles(files)
	if err != nil {
		return err
	}
	updated, err := change(doc)
	if err != nil {
		return err
	}
	if _, err := parseBugDocument(updated, doc.meta); err != nil {
		return fmt.Errorf("refuse invalid ledger update: %w", err)
	}
	return commitBugFiles(ctx, root, files, updated, doc.meta)
}

// commitBugFiles writes the sidecar before the ledger, so a crash in between
// never leaves a row without its metadata. It can leave a record no row
// references yet (readers ignore it; the next add of that ID replaces it) or a
// resolve's ResolvedAt ahead of its row. Unchanged files are not written.
func commitBugFiles(ctx context.Context, root *os.Root, files bugFiles, updated string, index bugMetaIndex) error {
	if files.metaExists || len(index) > 0 {
		meta, err := encodeBugMeta(index)
		if err != nil {
			return err
		}
		if !files.metaExists || !bytes.Equal(meta, files.meta) {
			if err := replaceBugFile(ctx, root, sidecarStoreFile, files.meta, files.metaExists, meta); err != nil {
				return err
			}
		}
	}
	if updated == string(files.ledger) {
		return nil
	}
	return replaceBugFile(ctx, root, ledgerStoreFile, files.ledger, true, []byte(updated))
}

// Stage and sync before replacement. A failed .pending file is retained; it
// blocks another mutation until inspected, rather than silently discarding data.
func replaceBugFile(ctx context.Context, root *os.Root, file bugStoreFile, before []byte, existed bool, updated []byte) error {
	perm, err := bugFilePerm(root, file.name, existed)
	if err != nil {
		return err
	}
	staged, err := root.OpenFile(file.pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("stage %s (inspect any retained %s): %w", file.name, file.pending, err)
	}
	_, writeErr := staged.Write(updated)
	if err := errors.Join(writeErr, staged.Chmod(perm), staged.Sync(), staged.Close()); err != nil {
		return err
	}
	if err := unchangedBugFile(ctx, root, file.name, before, existed); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(file.pending, file.name); err != nil {
		return err
	}
	return contextopt.SyncDirectory(ctx, root)
}

// bugFilePerm keeps an existing file's permissions; a new file is private.
func bugFilePerm(root *os.Root, name string, existed bool) (os.FileMode, error) {
	info, err := root.Lstat(name)
	if !existed {
		if errors.Is(err, os.ErrNotExist) {
			return 0o600, nil
		}
		return 0, errors.Join(fmt.Errorf("%s appeared during the update", name), err)
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s must be a regular file", name)
	}
	return info.Mode().Perm(), nil
}

// unchangedBugFile checks the file still holds what the update was based on.
func unchangedBugFile(ctx context.Context, root *os.Root, name string, before []byte, existed bool) error {
	if !existed {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s appeared before replacement; staged data retained", name)
		}
		return nil
	}
	current, err := contextopt.ReadRootSnapshot(ctx, root, name)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, current) {
		return fmt.Errorf("%s changed before replacement; staged data retained", name)
	}
	return nil
}
