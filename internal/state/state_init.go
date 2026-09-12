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
	files := [5]struct{ name, content string }{
		{"STATE.md", defaultStateMD()}, {"OPEN.md", defaultOpenMD()},
		{"BACKLOG.md", defaultBacklogMD()}, {"BUGS.md", defaultBugsMD()},
		{"QUESTIONS.md", defaultQuestionsMD()},
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := initializeLedgerFile(working, file.name, file.content); err != nil {
			return fmt.Errorf("initialize %s: %w", file.name, err)
		}
	}
	return nil
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

func initializeLedgerFile(root *os.Root, name, content string) error {
	info, err := root.Lstat(name)
	exists, err := regularLedgerFile(name, info, err)
	if err != nil || exists {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(content)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
