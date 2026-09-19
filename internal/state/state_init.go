package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// InitWorkingDir creates missing ledgers through pinned directories. Existing
// files are never truncated, and symlink ancestors are rejected before writes.
func InitWorkingDir(rootPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return initWorkingDir(ctx, rootPath)
}

// InitWorkingDirContext creates missing ledgers under the caller's cancellation
// and a bounded initialization deadline.
func InitWorkingDirContext(ctx context.Context, rootPath string) error {
	if ctx == nil {
		return errors.New("state initialization requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return initWorkingDir(ctx, rootPath)
}

func initWorkingDir(ctx context.Context, rootPath string) (err error) {
	project, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("open project for state initialization: %w", err)
	}
	defer func() { err = errors.Join(err, project.Close()) }()
	working, err := openWorkingDirectory(project)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	return initializeWorkingFiles(ctx, working)
}

func initializeWorkingFiles(ctx context.Context, working *os.Root) error {
	if err := working.Mkdir("evidence", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	info, err := working.Lstat("evidence")
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("evidence directory must not be a symlink or file")
	}
	for _, file := range ledgerTemplates() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := initializeLedgerFile(ctx, working, file.name, file.content); err != nil {
			return fmt.Errorf("initialize %s: %w", file.name, err)
		}
	}
	return nil
}

// ledgerTemplates is the one definition of which files an initialized ledger
// holds and what an empty one contains. Initialization, the audit and the
// ledgerless check all read this list rather than restating it (HISS-19).
func ledgerTemplates() [5]struct{ name, content string } {
	return [5]struct{ name, content string }{
		{"STATE.md", defaultStateMD()}, {"OPEN.md", defaultOpenMD()},
		{"BACKLOG.md", defaultBacklogMD()}, {"BUGS.md", defaultBugsMD()},
		{"QUESTIONS.md", defaultQuestionsMD()},
	}
}

// ledgerFileNames is ledgerTemplates for callers that need only the names.
func ledgerFileNames() [5]string {
	var names [5]string
	for i, file := range ledgerTemplates() {
		names[i] = file.name
	}
	return names
}

func openWorkingDirectory(project *os.Root) (*os.Root, error) {
	if err := project.Mkdir(WorkingDirName, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create working directory: %w", err)
	}
	before, err := project.Lstat(WorkingDirName)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("working directory must be a directory, never a symlink")
	}
	working, err := project.OpenRoot(WorkingDirName)
	if err != nil {
		return nil, err
	}
	opened, openErr := working.Stat(".")
	linked, linkErr := project.Lstat(WorkingDirName)
	if openErr != nil || linkErr != nil || !os.SameFile(before, opened) || !os.SameFile(before, linked) {
		return nil, errors.Join(fmt.Errorf("working directory changed while opening"), openErr, linkErr, working.Close())
	}
	return working, nil
}

// ledgerFileExists distinguishes absent files from inaccessible or invalid paths.
func ledgerFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	return regularLedgerFile(filepath.Base(path), info, err)
}

func regularLedgerFile(name string, info os.FileInfo, err error) (bool, error) {
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect ledger %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("ledger %s must be a regular file, not a symlink or directory", name)
	}
	return true, nil
}

// initializeLedgerFile writes the default content for one ledger file, and only
// when that file is absent. An existing regular file is left byte-for-byte
// alone, and a concurrent initializer winning the publish is not an error: the
// file it wrote is the file this call would have written.
//
// The write goes through contextopt.CreateRootSnapshot, which stages the content
// privately and publishes it with an exclusive hard link. Seeding is no longer
// arbitrated by a single Mkdir winner - every process that observes a ledgerless
// directory writes the five files - so an exclusive create alone would let a
// loser see the winner's empty, not-yet-written file, report success, and leave
// the audit that must run next reading a zero-byte ledger. Staged publication
// makes each name either absent or complete, never partial.
func initializeLedgerFile(ctx context.Context, root *os.Root, name, content string) error {
	info, err := root.Lstat(name)
	exists, err := regularLedgerFile(name, info, err)
	if err != nil || exists {
		return err
	}
	_, err = contextopt.CreateRootSnapshot(ctx, root, name, []byte(content), 0600)
	return err
}
