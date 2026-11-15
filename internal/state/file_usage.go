package state

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// LiveStateFileUsage measures actual bytes and parsed records, excluding fenced examples.
type LiveStateFileUsage struct {
	Name      string `json:"name"`
	Open      int    `json:"open"`
	Completed int    `json:"completed"`
	Bytes     int    `json:"bytes"`
	Present   bool   `json:"-"`
}

// liveStateFile holds one validated snapshot and the parser output shared by caps/archive.
type liveStateFile struct {
	usage    LiveStateFileUsage
	content  []byte
	exists   bool
	tasks    []taskLine
	bugs     *bugDocument
	bugFiles ledgerFiles
}

// liveStateFileCount bounds all shared ledger traversals.
const liveStateFileCount = 3

// liveStateFileNames fixes the bounded scan order for every cap and archive operation.
func liveStateFileNames() [liveStateFileCount]string {
	return [liveStateFileCount]string{openLedgerName, bugLedgerName, backlogLedgerName}
}

// MeasureLiveStateFiles computes all live-file counts and byte sizes through one parser path.
func MeasureLiveStateFiles(ctx context.Context, rootPath string) ([]LiveStateFileUsage, error) {
	files, err := readLiveStateFiles(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("measure live state files: %w", err)
	}
	usage := make([]LiveStateFileUsage, 0, len(files))
	for i := 0; i < len(files) && i < liveStateFileCount; i++ {
		usage = append(usage, files[i].usage)
	}
	return usage, nil
}

// readLiveStateFiles reads and validates every input before an archive may publish anything.
func readLiveStateFiles(ctx context.Context, rootPath string) ([]liveStateFile, error) {
	if ctx == nil {
		return nil, fmt.Errorf("live state files require a context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	root, err := ResolveWorkingDir(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve live state files: %w", err)
	}
	files := make([]liveStateFile, 0, liveStateFileCount)
	for _, name := range liveStateFileNames() {
		file, err := readLiveStateFile(ctx, root.RepoDir, name)
		if err != nil {
			return nil, fmt.Errorf("read live %s: %w", name, err)
		}
		files = append(files, file)
	}
	return files, nil
}

// readLiveStateFile counts through the same validated task/bug documents the commands use.
func readLiveStateFile(ctx context.Context, rootPath, name string) (file liveStateFile, err error) {
	file.usage.Name = name
	file.content, file.exists, err = contextopt.ObserveSnapshot(ctx, filepath.Join(rootPath, WorkingDirName, name))
	if err != nil || !file.exists {
		return file, err
	}
	file.usage.Present = true
	file.usage.Bytes = len(file.content)
	if name == bugLedgerName {
		return measureBugFile(ctx, rootPath, file)
	}
	file.tasks, err = parseLedgerTaskLines(name, strings.Split(string(file.content), "\n"))
	if err != nil {
		return file, err
	}
	for i := 0; i < len(file.tasks) && i < maxScannedLines; i++ {
		if file.tasks[i].completed {
			file.usage.Completed++
		} else {
			file.usage.Open++
		}
	}
	return file, nil
}

// measureBugFile resolves sidecar metadata against the very ledger bytes being measured.
func measureBugFile(ctx context.Context, rootPath string, file liveStateFile) (liveStateFile, error) {
	meta, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootPath, WorkingDirName, bugMetaName))
	if err != nil {
		return file, fmt.Errorf("read %s: %w", bugMetaName, err)
	}
	file.bugFiles = ledgerFiles{ledger: file.content, ledgerExists: true, meta: meta, metaExists: exists}
	file.bugs, err = parseBugFiles(file.bugFiles)
	if err != nil {
		return file, err
	}
	for i := 0; i < len(file.bugs.rows) && i < maxLedgerEntries; i++ {
		if file.bugs.rows[i].bug.Status == "resolved" {
			file.usage.Completed++
		} else {
			file.usage.Open++
		}
	}
	return file, nil
}
