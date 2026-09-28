package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// adoptBackupRoot holds the prior bytes of every existing file adoption replaces or merges
// into, one directory per run: <adoptBackupRoot>/<UTC stamp>/<repository path>. It lies below
// the private session ledger, which the managed .gitignore block ignores, so a backup never
// lands in the tree an adopter commits.
const adoptBackupRoot = workingDirPath + "/adopt-backups"

// backupStampLayout names a run's backup directory: UTC to the nanosecond, so two runs never
// share one, with no character a Windows path refuses (HISS-21).
const backupStampLayout = "20060102T150405.000000000Z"

// workingDirPerm is the owner-only mode of the private session ledger directory, the mode the
// ledger initialisation (internal/state) creates it with.
const workingDirPerm os.FileMode = 0o700

// Bounds of the line delta a replace entry carries: how many removed lines it quotes, and how
// many bytes of each.
const (
	maxDeltaQuotedLines = 3
	maxDeltaLineBytes   = 80
)

// replacement is one existing file adoption overwrites with bytes that are neither the file's
// current content nor an earlier Praetor text of it.
type replacement struct {
	rel    string // repository-relative slash path
	before []byte // the bytes adoption observed at rel
	after  []byte // the bytes that replace them
	detail string // what adoption writes, leading the report entry
	// merge marks after as before plus Praetor's entries with every adopter entry kept, such
	// as an editor JSON merge: the file is recorded as merged rather than replaced.
	merge bool
	// publish writes after; replaceExisting calls it only outside a dry run. replaceExistingAll
	// takes one publish for the whole set instead.
	publish func(ctx context.Context) error
}

// replaceExisting is the one path for overwriting adopter bytes: it keeps a backup of r.before
// (backupExisting), publishes r.after, and records the file as replaced, or as merged for
// r.merge, with a bounded line delta and the backup location. The backup comes first, so a
// refused backup leaves the file as it was. A dry run takes no backup, writes nothing and
// records the replacement it plans, with r.after as the bytes rel comes to (planDryRunWrite),
// so a later step, the branch ruleset preview among them, reads the file the run leaves rather
// than the one on disk.
func (s *adoptSession) replaceExisting(ctx context.Context, r replacement) error {
	return s.replaceExistingAll(ctx, []replacement{r}, r.publish)
}

// replaceExistingAll is replaceExisting for files one write publishes together, such as the
// DevContainer bundle or the vendor context files: it keeps a backup of every rs[i].before
// first, then runs publish once, then records each file as replaced. publish may also write
// files that replace nothing (created or unchanged ones), which the caller records itself, so
// it runs even when rs is empty. A refused backup leaves every file as it was; a dry run takes
// no backup, writes nothing and records the replacements it plans. rs[i].publish is unused.
func (s *adoptSession) replaceExistingAll(ctx context.Context, rs []replacement, publish func(ctx context.Context) error) error {
	if len(rs) > maxReportActions {
		return fmt.Errorf("adoption would replace %d files, above the %d a run reports", len(rs), maxReportActions)
	}
	notes := make([]string, len(rs))
	for i := 0; i < len(rs); i++ {
		note, err := s.backupExisting(ctx, rs[i].rel, rs[i].before)
		if err != nil {
			return err
		}
		notes[i] = note
	}
	if !s.opts.DryRun {
		if err := publish(ctx); err != nil {
			return err
		}
	}
	for i := 0; i < len(rs); i++ {
		s.planDryRunWrite(rs[i].rel, rs[i].after)
		delta := describeLineDelta(rs[i].before, rs[i].after)
		if rs[i].merge {
			s.report.recordReconciledAs(rs[i].rel, actionMerge, rs[i].detail+"; merged into existing content ("+delta+"); "+notes[i])
			continue
		}
		s.report.recordReplaced(rs[i].rel, rs[i].detail+"; replaced existing content ("+delta+"); "+notes[i])
	}
	return nil
}

// backupExisting keeps before, the bytes rel held, under this run's backup directory, and
// returns the report note naming it. The copy is written only when git confirms the backup path
// is ignored: otherwise a copy of adopter content would sit in the tree waiting to be committed,
// so there is no backup, the note says so and a warning names the reason. The write goes
// through the root-pinned writer, so a symlink at or below the backup root is refused before
// the caller replaces anything. A dry run checks the same and writes nothing.
func (s *adoptSession) backupExisting(ctx context.Context, rel string, before []byte) (string, error) {
	target := s.backupPath(rel)
	if err := checkBackupRoot(ctx, s.repoPath); err != nil {
		return "", err
	}
	ignored, err := util.GitIgnoredPaths(ctx, s.repoPath, []string{target}, false)
	if err != nil || !slices.Contains(ignored, target) {
		note := target + " is not ignored by git"
		reason := "git does not ignore " + target
		if err != nil {
			note = "git could not confirm " + target + " is ignored"
			reason = note + ": " + err.Error()
		}
		s.report.addWarning("%s: no backup of the prior bytes: %s", rel, reason)
		return "no backup: " + note, nil
	}
	if !s.opts.DryRun {
		if err := ensurePrivateWorkingDir(ctx, s.repoPath); err != nil {
			return "", fmt.Errorf("back up %s: %w", rel, err)
		}
		if err := contextopt.WriteSnapshotIn(ctx, s.repoPath, filepath.FromSlash(target), before, filePerm); err != nil {
			return "", fmt.Errorf("back up %s to %s: %w", rel, target, err)
		}
	}
	return "backup: " + target, nil
}

// ensurePrivateWorkingDir creates the private session ledger directory with owner-only access
// when it is still absent, so a backup written before the working-dir step (a fresh clone of an
// adopted repository, where /.workingdir/ is already ignored) does not create it world-readable
// at the writer's 0755 default; the ledger initialisation keeps an existing directory's mode.
// An existing entry is left as it is: the root-pinned backup writer refuses one that is a
// symlink or not a directory.
func ensurePrivateWorkingDir(ctx context.Context, repoPath string) error {
	root, err := contextopt.OpenDirectory(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	err = root.Mkdir(workingDirPath, workingDirPerm)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return errors.Join(fmt.Errorf("create %s: %w", workingDirPath, err), root.Close())
	}
	return root.Close()
}

// backupPath returns where this run keeps the backup of rel, fixing the run's stamp on first use.
func (s *adoptSession) backupPath(rel string) string {
	if s.backupStamp == "" {
		s.backupStamp = newBackupStamp()
	}
	return path.Join(adoptBackupRoot, s.backupStamp, rel)
}

// newBackupStamp names a new run's backup directory.
func newBackupStamp() string {
	return time.Now().UTC().Format(backupStampLayout)
}

// checkBackupRoot refuses a backup root that is a symlink, sits behind one or is not a
// directory, the paths the root-pinned writer refuses at write time. An absent root, or an
// absent private ledger above it, is accepted: the writer creates it.
func checkBackupRoot(ctx context.Context, repoPath string) error {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	dir, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.FromSlash(adoptBackupRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup root %s: %w", adoptBackupRoot, err)
	}
	return dir.Close()
}

// preflightForceBackupRoot refuses, under --force and before the first step writes anything, a
// backup root checkBackupRoot refuses. A --force replace of a drifted scaffold (replaceScaffold)
// backs up to that root, and the scaffolds it may replace belong to nearly every step, so the
// root is checked whenever --force is set instead of per planned replace; checked only at the
// replace, the refusal came after every earlier step had written. Without --force only a hook
// merge and a hand-edited vendor context file take a backup, and preflightAgentHooks and
// preflightVendorBackupRoot check the root for them.
func preflightForceBackupRoot(ctx context.Context, s *adoptSession) error {
	if !s.opts.Force {
		return nil
	}
	if err := checkBackupRoot(ctx, s.repoPath); err != nil {
		return fmt.Errorf("--force preflight: %w", err)
	}
	return nil
}

// describeLineDelta renders the line delta from before to after for a report entry: the counts,
// then the first removed lines, each cut to a bounded length and quoted.
func describeLineDelta(before, after []byte) string {
	delta := util.LineDeltaOf(string(before), string(after), maxDeltaQuotedLines)
	summary := "-" + strconv.Itoa(delta.Removed) + "/+" + strconv.Itoa(delta.Added) + " lines"
	if !delta.Changed() {
		// LineDeltaOf compares LF text, so a replace with no line delta changed line endings alone.
		return summary + ", line endings only"
	}
	if len(delta.RemovedLines) == 0 {
		return summary
	}
	quoted := make([]string, 0, len(delta.RemovedLines))
	for i := 0; i < len(delta.RemovedLines) && i < maxDeltaQuotedLines; i++ {
		quoted = append(quoted, strconv.Quote(util.TruncateExcerpt(delta.RemovedLines[i], maxDeltaLineBytes)))
	}
	summary += ", removed " + strings.Join(quoted, ", ")
	if more := delta.Removed - len(quoted); more > 0 {
		summary += " and " + strconv.Itoa(more) + " more"
	}
	return summary
}

// legacyHookBackupWarning reports a <file>.bak an earlier adoption wrote beside a hook file.
// Adoption no longer writes one and never deletes adopter data, so it only says the copy is
// there and could be committed by accident.
func legacyHookBackupWarning(s *adoptSession, rel string) {
	legacy := rel + hookBackupExt
	if _, err := os.Lstat(filepath.Join(s.repoPath, filepath.FromSlash(legacy))); err != nil {
		return
	}
	s.report.addWarning("%s: backup an earlier adoption wrote beside %s; adoption no longer writes or removes it. "+
		"Review it and delete it, or it may be committed by accident (backups now go to %s)", legacy, rel, adoptBackupRoot)
}
