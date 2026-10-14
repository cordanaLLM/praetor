package state

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// LegacyWorkingDirFinding returns the finding for a repository whose legacy folder
// (LegacyWorkingDirName) still holds content beside WorkingDirName, and "" when there is
// none: no legacy folder, or an empty one. It names the OPEN.md each folder holds and its
// rows, so the stub ledger and the populated one are both visible (#1233). State audit
// reports it as a violation; adoption reports it as a warning.
func LegacyWorkingDirFinding(rootPath string) (string, error) {
	held, err := legacyFolderHolds(rootPath)
	if err != nil || !held {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return fmt.Sprintf("legacy private state folder %s/ holds content beside %s/ (%s; %s): run 'praetorctl state migrate %s' to review the move, then add --apply",
		LegacyWorkingDirName, WorkingDirName, describeOpenLedger(ctx, rootPath, WorkingDirName),
		describeOpenLedger(ctx, rootPath, LegacyWorkingDirName), rootPath), nil
}

// legacyFolderHolds reports whether the legacy folder exists and holds at least one entry. An
// entry of that name that is not a directory counts as content: migration refuses it.
func legacyFolderHolds(rootPath string) (held bool, err error) {
	legacy := filepath.Join(rootPath, LegacyWorkingDirName)
	info, err := os.Lstat(legacy)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", LegacyWorkingDirName, err)
	}
	if !info.IsDir() {
		return true, nil
	}
	dir, err := os.Open(legacy)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", LegacyWorkingDirName, err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	names, err := dir.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s: %w", LegacyWorkingDirName, err)
	}
	return len(names) > 0, nil
}

// describeOpenLedger states what folder/OPEN.md holds, counted by the task parser.
func describeOpenLedger(ctx context.Context, rootPath, folder string) string {
	name := folder + "/" + openLedgerName
	content, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootPath, folder, openLedgerName))
	if errors.Is(err, fs.ErrNotExist) {
		return name + " absent"
	}
	if err != nil {
		return name + " unreadable: " + err.Error()
	}
	tasks, err := parseLedgerTaskLines(openLedgerName, strings.Split(string(content), "\n"))
	if err != nil {
		return fmt.Sprintf("%s %d bytes, unparsable: %v", name, len(content), err)
	}
	open := 0
	for _, task := range tasks {
		if !task.completed {
			open++
		}
	}
	return fmt.Sprintf("%s %d bytes, %d open of %d rows", name, len(content), open, len(tasks))
}
