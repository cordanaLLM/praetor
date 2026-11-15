package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// ArchiveCompletedTasksContext previews all three live ledgers; apply publishes archives
// before replacing any live snapshot. No completed OPEN row is moved to BACKLOG.md.
func ArchiveCompletedTasksContext(ctx context.Context, rootPath string, apply bool) ([]FileArchiveReport, error) {
	return archiveCompletedTasksAt(ctx, rootPath, apply, time.Now().UTC())
}

// archiveCompletedTasksAt fixes the fallback month for one bounded archive operation.
func archiveCompletedTasksAt(ctx context.Context, rootPath string, apply bool, now time.Time) ([]FileArchiveReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("state task archive requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	files, err := readLiveStateFiles(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("plan state task archive: %w", err)
	}
	plans := make([]taskArchivePlan, 0, len(files))
	reports := make([]FileArchiveReport, 0, len(files))
	for i := 0; i < len(files) && i < liveStateFileCount; i++ {
		plan, err := planTaskArchive(files[i], now)
		if err != nil {
			return nil, err
		}
		plans, reports = append(plans, plan), append(reports, plan.report)
	}
	if !apply {
		return reports, nil
	}
	if err := applyTaskArchive(ctx, ledgerRoot(ctx, rootPath), plans); err != nil {
		return nil, fmt.Errorf("apply state task archive: %w", err)
	}
	_, err = CheckStateFileCaps(ctx, rootPath, true)
	return reports, err
}

// applyTaskArchive completes every archive write before it removes any live record.
func applyTaskArchive(ctx context.Context, rootPath string, plans []taskArchivePlan) error {
	for i := 0; i < len(plans) && i < liveStateFileCount; i++ {
		if err := publishTaskArchive(ctx, rootPath, plans[i]); err != nil {
			return err
		}
	}
	for i := 0; i < len(plans) && i < liveStateFileCount; i++ {
		plan := plans[i]
		if plan.report.Entries == 0 {
			continue
		}
		if err := reserveArchivedBugIDs(ctx, rootPath, plan); err != nil {
			return err
		}
		path := filepath.Join(rootPath, WorkingDirName, plan.file.usage.Name)
		options := contextopt.ReplaceOptions{Expected: plan.file.content, Exists: true, Mode: 0o600}
		if err := contextopt.ReplaceSnapshot(ctx, path, plan.after, options); err != nil {
			return fmt.Errorf("replace live %s (archive retained): %w", plan.file.usage.Name, err)
		}
	}
	return nil
}

// openTaskArchiveDirectory creates the private archive directory without following symlinks.
func openTaskArchiveDirectory(ctx context.Context, rootPath string) (archive *os.Root, err error) {
	working, err := contextopt.OpenDirectoryIn(ctx, rootPath, WorkingDirName)
	if err != nil {
		return nil, fmt.Errorf("open archive parent: %w", err)
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	if err := working.Mkdir("archive", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create state archive directory: %w", err)
	}
	return contextopt.OpenDirectoryIn(ctx, rootPath, filepath.Join(WorkingDirName, "archive"))
}

// publishTaskArchive appends each bounded monthly group using compare-and-swap snapshots.
func publishTaskArchive(ctx context.Context, rootPath string, plan taskArchivePlan) (err error) {
	if plan.report.Entries == 0 {
		return nil
	}
	archive, err := openTaskArchiveDirectory(ctx, rootPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, archive.Close()) }()
	months := make([]string, 0, len(plan.groups))
	for month := range plan.groups {
		months = append(months, month)
	}
	slices.Sort(months)
	for i := 0; i < len(months) && i < maxScannedLines; i++ {
		month := months[i]
		name := strings.TrimSuffix(plan.file.usage.Name, ".md") + "-" + month + ".md"
		if err := appendTaskArchive(ctx, archive, name, plan.file.usage.Name, plan.groups[month]); err != nil {
			return fmt.Errorf("write archive %s: %w", name, err)
		}
	}
	return nil
}

// appendTaskArchive creates a readable header once and appends every record, including duplicates.
func appendTaskArchive(ctx context.Context, root *os.Root, name, source string, records []string) error {
	before, exists, err := contextopt.ObserveRootSnapshot(ctx, root, name)
	if err != nil {
		return err
	}
	var updated strings.Builder
	updated.Write(before)
	prefix, err := taskArchivePrefix(before, source)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	updated.WriteString(prefix)
	for i := 0; i < len(records) && i < maxScannedLines; i++ {
		updated.WriteString(records[i])
		if !strings.HasSuffix(records[i], "\n") {
			updated.WriteByte('\n')
		}
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o600}
	return contextopt.ReplaceRootSnapshot(ctx, root, name, []byte(updated.String()), options)
}

// taskArchivePrefix adds a header to a new/empty archive and validates existing bug headers.
func taskArchivePrefix(before []byte, source string) (string, error) {
	if len(before) == 0 {
		header := fmt.Sprintf("# %s archive\n\n", source)
		if source == bugLedgerName {
			header += bugTableHeader + "\n" + bugTableSeparator + "\n"
		}
		return header, nil
	}
	if source == bugLedgerName && !strings.Contains(string(before), bugTableHeader+"\n"+bugTableSeparator) {
		return "", fmt.Errorf("archive requires a bug table header")
	}
	if before[len(before)-1] != '\n' {
		return "\n", nil
	}
	return "", nil
}

// reserveArchivedBugIDs retains metadata and IDs so new bugs never reuse an archived ID.
func reserveArchivedBugIDs(ctx context.Context, rootPath string, plan taskArchivePlan) error {
	if plan.file.bugs == nil {
		return nil
	}
	index := plan.file.bugs.meta
	for i := 0; i < len(plan.file.bugs.rows) && i < maxLedgerEntries; i++ {
		bug := plan.file.bugs.rows[i].bug
		index[bug.ID] = metadataOf(bug)
	}
	updated, err := encodeBugMeta(index)
	if err != nil {
		return fmt.Errorf("reserve archived bug IDs: %w", err)
	}
	files := plan.file.bugFiles
	if bytes.Equal(updated, files.meta) {
		return nil
	}
	options := contextopt.ReplaceOptions{Expected: files.meta, Exists: files.metaExists, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(ctx, filepath.Join(rootPath, WorkingDirName, bugMetaName), updated, options); err != nil {
		return fmt.Errorf("reserve archived bug IDs: %w", err)
	}
	return nil
}
