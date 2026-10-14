package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// Bounds of one legacy folder migration (HISS-02). The walk skips registered worktrees, so
// maxLegacyEntries counts ledgers, evidence and scratch files, never whole checkouts.
const (
	legacyMigrationTimeout = 5 * time.Minute
	maxLegacyEntries       = 100000
	maxLegacyWorktrees     = 4096
	maxLegacyFileBytes     = int64(1) << 32
	maxTrackedReferences   = 200
	trackedReferenceBytes  = 1 << 20
)

// ErrLegacyMigrationConflict reports a migration refused because a legacy file and the
// .workingdir file of the same name differ and no merge resolves them. Nothing was written.
var ErrLegacyMigrationConflict = errors.New("legacy state migration refused")

// LegacyMigrationOptions selects what MigrateLegacyWorkingDir does.
type LegacyMigrationOptions struct {
	// Apply writes the plan. Without it the call is a dry run that writes nothing.
	Apply bool
	// Merge resolves a same-name ledger that differs between the folders: a .workingdir
	// ledger that still holds only its empty template takes the legacy file, and a populated
	// OPEN.md or BACKLOG.md gets the legacy file appended. Without it every differing
	// same-name file is a conflict.
	Merge bool
}

// LegacyLedgerMerge describes one merged ledger and the rows the merged file shows, counted
// by the parser every task command reads it with.
type LegacyLedgerMerge struct {
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	OpenTasks      int    `json:"open_tasks"`
	CompletedTasks int    `json:"completed_tasks"`
}

// LegacyMigrationReport lists what a migration of LegacyWorkingDirName moves, or moved.
// Paths are slash-separated and relative to the folder.
type LegacyMigrationReport struct {
	LegacyPresent bool                `json:"legacy_present"`
	Applied       bool                `json:"applied"`
	Moved         []string            `json:"moved,omitempty"`
	Duplicates    []string            `json:"duplicates,omitempty"`
	Merged        []LegacyLedgerMerge `json:"merged,omitempty"`
	Worktrees     []string            `json:"worktrees,omitempty"`
	Conflicts     []string            `json:"conflicts,omitempty"`
	// TrackedReferences lists tracked files that still name the legacy folder.
	TrackedReferences []string `json:"tracked_references,omitempty"`
	LegacyRemoved     bool     `json:"legacy_removed"`
}

type legacyAction int

const (
	legacyMove legacyAction = iota
	legacyDuplicate
	legacyMerge
	legacyWorktree
)

// legacyItem is one planned step. digest is the SHA-256 of the legacy file the plan read;
// apply refuses a file that no longer matches it. A merge replaces expected with merged.
type legacyItem struct {
	rel      string
	action   legacyAction
	digest   string
	expected []byte
	merged   []byte
}

type legacyPlan struct {
	root, legacy, working string
	opts                  LegacyMigrationOptions
	items                 []legacyItem
	dirs                  []string
	report                *LegacyMigrationReport
}

// MigrateLegacyWorkingDir moves the content of LegacyWorkingDirName into WorkingDirName
// (#1233). It plans the whole move first: a file absent from .workingdir moves, a file
// identical in both drops its legacy copy, a registered Git worktree moves with
// 'git worktree move', and a same-name file that differs is a conflict unless
// opts.Merge resolves it. Any conflict refuses the migration with nothing written; existing
// .workingdir files are never overwritten except by a requested ledger merge. Each move is
// verified by SHA-256 before its legacy copy goes, and the legacy folder is removed only
// once it is empty. A repository without the folder is a no-op.
func MigrateLegacyWorkingDir(ctx context.Context, rootPath string, opts LegacyMigrationOptions) (*LegacyMigrationReport, error) {
	if ctx == nil {
		return nil, errors.New("legacy state migration requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, legacyMigrationTimeout)
	defer cancel()
	report := &LegacyMigrationReport{}
	present, err := legacyFolderPresent(rootPath)
	if err != nil || !present {
		return report, err
	}
	report.LegacyPresent = true
	plan, err := planLegacyMigration(ctx, rootPath, opts, report)
	if err != nil {
		return report, err
	}
	if len(report.Conflicts) > 0 {
		return report, fmt.Errorf("%w: %d conflicting files; nothing was written", ErrLegacyMigrationConflict, len(report.Conflicts))
	}
	if !opts.Apply {
		return report, nil
	}
	return report, plan.apply(ctx)
}

// legacyFolderPresent reports whether rootPath holds the legacy folder. An entry of that
// name that is not a directory, a symbolic link included, is refused rather than followed.
func legacyFolderPresent(rootPath string) (bool, error) {
	info, err := os.Lstat(filepath.Join(rootPath, LegacyWorkingDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", LegacyWorkingDirName, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s in %s is not a directory; nothing was moved", LegacyWorkingDirName, rootPath)
	}
	return true, nil
}

func planLegacyMigration(ctx context.Context, rootPath string, opts LegacyMigrationOptions, report *LegacyMigrationReport) (*legacyPlan, error) {
	plan := &legacyPlan{
		root: rootPath, legacy: filepath.Join(rootPath, LegacyWorkingDirName),
		working: filepath.Join(rootPath, WorkingDirName), opts: opts, report: report,
	}
	if info, err := os.Lstat(plan.working); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("%s in %s is not a directory; nothing was moved", WorkingDirName, rootPath)
	}
	worktrees, err := legacyWorktrees(ctx, rootPath, plan.legacy)
	if err != nil {
		return nil, err
	}
	if err := plan.walk(ctx, worktrees); err != nil {
		return nil, err
	}
	report.TrackedReferences, err = trackedLegacyReferences(ctx, rootPath)
	return plan, err
}

func (p *legacyPlan) legacyPath(rel string) string {
	return filepath.Join(p.legacy, filepath.FromSlash(rel))
}

func (p *legacyPlan) workingPath(rel string) string {
	return filepath.Join(p.working, filepath.FromSlash(rel))
}

func (p *legacyPlan) conflict(rel, reason string) {
	p.report.Conflicts = append(p.report.Conflicts, rel+": "+reason)
}

func (p *legacyPlan) walk(ctx context.Context, worktrees map[string]bool) error {
	entries := 0
	return filepath.WalkDir(p.legacy, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries > maxLegacyEntries {
			return fmt.Errorf("%s holds more than %d entries; migrate it in parts", LegacyWorkingDirName, maxLegacyEntries)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(p.legacy, path)
		if err != nil || rel == "." {
			return err
		}
		return p.visit(ctx, filepath.ToSlash(rel), entry, worktrees)
	})
}

func (p *legacyPlan) visit(ctx context.Context, rel string, entry fs.DirEntry, worktrees map[string]bool) error {
	switch {
	case entry.IsDir() && worktrees[rel]:
		p.planWorktree(rel)
		return filepath.SkipDir
	case entry.IsDir():
		p.dirs = append(p.dirs, rel)
		return nil
	case entry.Type().IsRegular():
		return p.planFile(ctx, rel)
	}
	p.conflict(rel, "is neither a regular file nor a directory; move it by hand")
	return nil
}

func (p *legacyPlan) planWorktree(rel string) {
	if _, err := os.Lstat(p.workingPath(rel)); !errors.Is(err, fs.ErrNotExist) {
		p.conflict(rel, "registered worktree whose destination in "+WorkingDirName+"/ already exists")
		return
	}
	p.items = append(p.items, legacyItem{rel: rel, action: legacyWorktree})
	p.report.Worktrees = append(p.report.Worktrees, rel)
}

func (p *legacyPlan) planFile(ctx context.Context, rel string) error {
	digest, _, err := contextopt.DigestBinarySnapshot(ctx, p.legacyPath(rel), maxLegacyFileBytes)
	if err != nil {
		return fmt.Errorf("digest %s/%s: %w", LegacyWorkingDirName, rel, err)
	}
	info, err := os.Lstat(p.workingPath(rel))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.items = append(p.items, legacyItem{rel: rel, action: legacyMove, digest: digest})
		p.report.Moved = append(p.report.Moved, rel)
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s/%s: %w", WorkingDirName, rel, err)
	case !info.Mode().IsRegular():
		p.conflict(rel, "the "+WorkingDirName+"/ entry of that name is not a regular file")
		return nil
	}
	current, _, err := contextopt.DigestBinarySnapshot(ctx, p.workingPath(rel), maxLegacyFileBytes)
	if err != nil {
		return fmt.Errorf("digest %s/%s: %w", WorkingDirName, rel, err)
	}
	if current == digest {
		p.items = append(p.items, legacyItem{rel: rel, action: legacyDuplicate, digest: digest})
		p.report.Duplicates = append(p.report.Duplicates, rel)
		return nil
	}
	return p.planDiffering(ctx, rel)
}

// planDiffering resolves a same-name file whose bytes differ: a conflict unless a merge was
// asked for and mergedLedger can merge it without losing either side.
func (p *legacyPlan) planDiffering(ctx context.Context, rel string) error {
	if !p.opts.Merge || !isLedgerFile(rel) {
		p.conflict(rel, differingReason(rel, p.opts.Merge))
		return nil
	}
	legacy, err := contextopt.ReadSnapshot(ctx, p.legacyPath(rel))
	if err != nil {
		return fmt.Errorf("read %s/%s: %w", LegacyWorkingDirName, rel, err)
	}
	current, err := contextopt.ReadSnapshot(ctx, p.workingPath(rel))
	if err != nil {
		return fmt.Errorf("read %s/%s: %w", WorkingDirName, rel, err)
	}
	merged, mode, ok := mergedLedger(rel, current, legacy)
	if !ok {
		p.conflict(rel, "holds entries in both folders, and only OPEN.md and BACKLOG.md merge; reconcile it by hand")
		return nil
	}
	open, completed, err := mergedTaskCounts(rel, merged)
	if err != nil {
		p.conflict(rel, "merged ledger does not parse: "+err.Error())
		return nil
	}
	sum := sha256.Sum256(legacy)
	p.items = append(p.items, legacyItem{rel: rel, action: legacyMerge, digest: hex.EncodeToString(sum[:]), expected: current, merged: merged})
	p.report.Merged = append(p.report.Merged, LegacyLedgerMerge{Name: rel, Mode: mode, OpenTasks: open, CompletedTasks: completed})
	return nil
}

func differingReason(rel string, merge bool) string {
	switch {
	case !isLedgerFile(rel):
		return "differs from " + WorkingDirName + "/" + rel + "; keep one copy by hand (rename the legacy file to move it beside the other)"
	case merge:
		return "differs and cannot be merged"
	}
	return "ledger differs from " + WorkingDirName + "/" + rel + "; rerun with --merge to merge it"
}

// isLedgerFile reports whether rel names one of the top-level ledger files.
func isLedgerFile(rel string) bool {
	_, ok := ledgerTemplate(rel)
	return ok
}

func ledgerTemplate(rel string) (string, bool) {
	for _, file := range ledgerTemplates() {
		if file.name == rel {
			return file.content, true
		}
	}
	return "", false
}

// legacyMergeMarker separates the appended legacy ledger from the rows already in place.
const legacyMergeMarker = "<!-- migrated from " + LegacyWorkingDirName + "/%s by praetorctl state migrate -->\n"

// mergedLedger merges a legacy ledger into the current one without dropping a byte of
// either. A current ledger that still holds only its empty template takes the legacy file;
// a populated OPEN.md or BACKLOG.md keeps its rows and gets the legacy file appended below a
// marker. Any other populated ledger has IDs or a sync marker of its own and is not merged.
func mergedLedger(rel string, current, legacy []byte) ([]byte, string, bool) {
	template, _ := ledgerTemplate(rel)
	normalized, _ := util.NormalizeLineEndings(string(current))
	if normalized == template {
		return legacy, "replaced-template", true
	}
	if rel != openLedgerName && rel != backlogLedgerName {
		return nil, "", false
	}
	merged := string(current)
	if !strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	merged += "\n" + fmt.Sprintf(legacyMergeMarker, rel) + string(legacy)
	return []byte(merged), "appended", true
}

// mergedTaskCounts counts the rows a merged task ledger shows, through the parser the task
// commands use; other ledgers carry no task rows.
func mergedTaskCounts(rel string, merged []byte) (open, completed int, err error) {
	if rel != openLedgerName && rel != backlogLedgerName {
		return 0, 0, nil
	}
	tasks, err := parseLedgerTaskLines(rel, strings.Split(string(merged), "\n"))
	if err != nil {
		return 0, 0, err
	}
	for _, task := range tasks {
		if task.completed {
			completed++
		} else {
			open++
		}
	}
	return open, completed, nil
}

// legacyWorktrees returns the registered Git worktrees inside legacy, slash-separated and
// relative to it. Outside a Git worktree there are none.
func legacyWorktrees(ctx context.Context, rootPath, legacy string) (map[string]bool, error) {
	present, err := util.GitWorktreePresent(ctx, rootPath)
	if err != nil || !present {
		return nil, err
	}
	listed, err := worktree.NewManager(rootPath).List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}
	base, err := canonicalLegacyPath(legacy)
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool)
	for i := 0; i < len(listed) && i < maxLegacyWorktrees; i++ {
		if rel, ok := legacyRelative(base, listed[i].Path); ok {
			found[rel] = true
		}
	}
	return found, nil
}

func canonicalLegacyPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// legacyRelative returns listed relative to base when it lies strictly inside it.
func legacyRelative(base, listed string) (string, bool) {
	canonical, err := canonicalLegacyPath(filepath.FromSlash(listed))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(base, canonical)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// trackedLegacyReferences lists tracked text files that still name the legacy folder.
func trackedLegacyReferences(ctx context.Context, rootPath string) ([]string, error) {
	present, err := util.GitWorktreePresent(ctx, rootPath)
	if err != nil || !present {
		return nil, err
	}
	result, status, err := util.RunGitProbeStatus(ctx, rootPath, trackedReferenceBytes,
		"grep", "-l", "-z", "-I", "-F", "-e", LegacyWorkingDirName, "--")
	if err != nil {
		return nil, fmt.Errorf("search tracked files for %s: %w", LegacyWorkingDirName, util.CommandDiagnostic(err, result.Stderr))
	}
	if status == 1 {
		return nil, nil
	}
	var files []string
	for _, name := range strings.Split(string(result.Stdout), "\x00") {
		if name != "" && len(files) < maxTrackedReferences {
			files = append(files, name)
		}
	}
	return files, nil
}
