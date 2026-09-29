package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Action values recorded in ActionDetail.
const (
	actionCreate    = "create"
	actionReconcile = "reconcile"
	actionMerge     = "merge"
	actionAppend    = "append"
	actionReplace   = "replace"
	actionRemove    = "remove"
	actionSkip      = "skip"
)

// recordCreated lists path as created (or planned in dry-run mode).
func (r *AdoptReport) recordCreated(path, details string) {
	r.CreatedFiles = append(r.CreatedFiles, path)
	r.ActionDetails = append(r.ActionDetails, ActionDetail{Path: path, Action: actionCreate, Details: details})
}

// recordReconciled lists path as reconciled with the default reconcile action.
func (r *AdoptReport) recordReconciled(path, details string) {
	r.recordReconciledAs(path, actionReconcile, details)
}

// recordReconciledAs lists path as reconciled with an explicit action (merge, append).
func (r *AdoptReport) recordReconciledAs(path, action, details string) {
	r.ReconciledFiles = append(r.ReconciledFiles, path)
	r.ActionDetails = append(r.ActionDetails, ActionDetail{Path: path, Action: action, Details: details})
}

// recordReplaced lists path as reconciled with the replace action: existing bytes that were
// neither current nor earlier Praetor text were overwritten (replaceExisting).
func (r *AdoptReport) recordReplaced(path, details string) {
	r.recordReconciledAs(path, actionReplace, details)
}

// maxReportActions bounds a walk over a report's action entries (HISS-02); a run records a few
// hundred at most.
const maxReportActions = 1 << 16

// Replaced returns the replace entries of the report, in the order they were recorded: the
// existing files whose adopter bytes adoption overwrote, or plans to overwrite in a dry run.
func (r *AdoptReport) Replaced() []ActionDetail {
	replaced := make([]ActionDetail, 0)
	for i := 0; i < len(r.ActionDetails) && i < maxReportActions; i++ {
		if r.ActionDetails[i].Action == actionReplace {
			replaced = append(replaced, r.ActionDetails[i])
		}
	}
	return replaced
}

// ReconciledNotReplaced returns the reconciled files that have no replace entry, in report
// order: the files adoption verified, merged or refreshed without overwriting adopter bytes.
// Every report surface (CLI, MCP) lists these apart from Replaced, so a replaced file shows
// once, with its line delta and backup, and never beside a file that was only verified.
func (r *AdoptReport) ReconciledNotReplaced() []string {
	replaced := r.Replaced()
	skip := make(map[string]bool, len(replaced))
	for _, entry := range replaced {
		skip[entry.Path] = true
	}
	kept := make([]string, 0, len(r.ReconciledFiles))
	for i := 0; i < len(r.ReconciledFiles) && i < maxReportActions; i++ {
		if !skip[r.ReconciledFiles[i]] {
			kept = append(kept, r.ReconciledFiles[i])
		}
	}
	return kept
}

// recordSkipped records a deliberate safety skip as both an action and a warning.
func (r *AdoptReport) recordSkipped(path, details string) {
	r.recordNotApplicable(path, details)
	r.Warnings = append(r.Warnings, path+": "+details)
}

// recordNotApplicable records a surface the manifest's selection leaves out. It is an action,
// not a warning: nothing was refused for safety, the repository declared it does not use it.
func (r *AdoptReport) recordNotApplicable(path, details string) {
	r.ActionDetails = append(r.ActionDetails, ActionDetail{Path: path, Action: actionSkip, Details: details})
}

// addError records a non-fatal failure that the caller must surface.
func (r *AdoptReport) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// addWarning records advisory information that does not fail the adoption.
func (r *AdoptReport) addWarning(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// scaffold describes one file that adoption creates when it is missing.
type scaffold struct {
	rel     string      // repository-relative path
	perm    os.FileMode // file mode
	content []byte      // payload written when the file is created
	// auditLocked marks a file whose bytes praetorctl audit compares with the scaffold: the
	// managed asset families (managed_family.go) and the branch ruleset while the policy
	// requires one (rulesetRequired). Audit fails until such a file holds the scaffold, so
	// --force may overwrite a drifted copy (replaceExisting). Every other scaffold is generated
	// but not audit-verified: a drifted copy is the repository's and is kept, --force included,
	// with a warning; deleting it and re-running adopt regenerates it.
	auditLocked bool
	created     string // action detail when the file is (or would be) written
	verified    string // action detail when an existing file matches content
	// confined writes through contextopt.WriteSnapshotIn, the writer compile-context uses, which
	// follows no symlink below the repository, and refreshes an earlier text through
	// contextopt.ReplaceSnapshotIn. It is set for the files compile-context also reads (the
	// canonical personas); every other scaffold goes through writeRepoFile.
	confined bool
	// prior holds the digest (priorRendering) of every text a Praetor release wrote at rel,
	// keyed to what produced it. An existing file holding one of them, in one consistent
	// line-ending style, is Praetor output nobody edited, so adoption refreshes it to content
	// in that style without --force, as it migrates an earlier lefthook.yml
	// (isPriorLefthookConfig). An edited copy matches no digest and keeps the contract above. A
	// set may hold the current text too: a file that already holds content is verified, never
	// refreshed. A rendered scaffold computes its one earlier text at run time instead, such as
	// the branch ruleset of the repository as the run found it (forge.PriorRulesetDigests).
	prior map[string]string
	// refreshed is the action detail when an earlier Praetor text is refreshed.
	refreshed string
}

// priorRendering reports whether data is one of the earlier Praetor texts digests names, and
// whether data is its CRLF checkout. Every earlier-text set in adoption (labels, lefthook.yml,
// the pinned catalog) is read with it, and the managed asset families' Prior with the same
// util.LookupCanonicalText: a key is util.CanonicalTextDigest of the recorded LF text, so a
// checkout that converted it to CRLF (core.autocrlf on Windows) is still recognised, while an
// edit, a lost final newline or mixed line endings match nothing (HISS-21).
func priorRendering(data []byte, digests map[string]string) (known, crlf bool) {
	_, known, crlf = util.LookupCanonicalText(data, digests)
	return known, crlf
}

// isPriorRendering reports whether data is one of the earlier Praetor texts digests names, in
// either consistent line-ending style (priorRendering).
func isPriorRendering(data []byte, digests map[string]string) bool {
	known, _ := priorRendering(data, digests)
	return known
}

// scaffoldState is what scaffoldFile did with one file, or what it found there.
type scaffoldState int

const (
	// scaffoldWritten: the file was created, refreshed from an earlier Praetor text, or replaced
	// under --force (planned in a dry run).
	scaffoldWritten scaffoldState = iota + 1
	// scaffoldIdentical: an existing file matches the scaffold, line endings aside.
	scaffoldIdentical
	// scaffoldDrifted: an existing file differs from the scaffold and was preserved.
	scaffoldDrifted
	// scaffoldUnverified: an existing file could not be read for comparison and was preserved.
	scaffoldUnverified
)

// write persists data unless the session is a dry run.
func (s *adoptSession) write(path string, data []byte, perm os.FileMode) error {
	if s.opts.DryRun {
		return nil
	}
	return writeRepoFile(path, data, perm)
}

// scaffoldFile creates sc.rel when it is missing. An existing file is compared with sc.content
// first: an earlier Praetor text of it is refreshed, a match is reported as sc.verified, and a
// difference is reported as drift rather than overwritten or passed off as verified. Only when
// Force is set and audit locks the scaffold (auditLocked) is a drifted file overwritten, through
// replaceExisting, so the report lists it as replaced with its line delta and backup, never as
// created.
func (s *adoptSession) scaffoldFile(ctx context.Context, sc scaffold) (scaffoldState, error) {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return 0, err
	}
	if fileExists(full) {
		refreshed, err := s.refreshPriorScaffold(ctx, full, sc)
		if err != nil || refreshed {
			return scaffoldWritten, err
		}
		return s.recordExistingScaffold(ctx, full, sc)
	}
	if err := s.writeScaffold(ctx, full, sc); err != nil {
		return 0, err
	}
	s.planDryRunWrite(sc.rel, sc.content)
	s.report.recordCreated(sc.rel, sc.created)
	return scaffoldWritten, nil
}

// refreshPriorScaffold replaces an existing file holding an earlier Praetor text of sc with
// sc.content in the file's own line-ending style (replacePriorText), and reports whether it did. A file that already holds sc.content, line endings aside, is left to
// recordExistingScaffold to verify, and one that cannot be read to report as unverified. A dry
// run reports the refresh it would make.
func (s *adoptSession) refreshPriorScaffold(ctx context.Context, full string, sc scaffold) (bool, error) {
	if len(sc.prior) == 0 {
		return false, nil
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists {
		return false, ctx.Err()
	}
	known, crlf := priorRendering(actual, sc.prior)
	if !known {
		return false, nil
	}
	if current, err := util.CanonicalTextEquivalent(actual, sc.content); err == nil && current {
		return false, nil
	}
	return true, s.replacePriorText(ctx, full, actual, crlf, sc, sc.refreshed)
}

// replacePriorText replaces actual, an unedited earlier Praetor text of sc at full, with
// sc.content in actual's line-ending style (crlf) and records detail. A text file's replacement
// is bound to the observed bytes (publishPriorRefresh), so a file edited in between is not
// overwritten. A dry run records the refresh it would make. It is the one refresh both the
// scaffold earlier-text sets (refreshPriorScaffold) and the managed asset families
// (reconcileManagedFamilyFile) use.
func (s *adoptSession) replacePriorText(ctx context.Context, full string, actual []byte, crlf bool, sc scaffold, detail string) error {
	content := []byte(util.RestoreLineEndings(string(sc.content), crlf))
	if !s.opts.DryRun {
		if err := s.publishPriorRefresh(ctx, full, actual, content, sc); err != nil {
			return fmt.Errorf("refresh %s: %w", sc.rel, err)
		}
	}
	s.planDryRunWrite(sc.rel, content)
	s.report.recordReconciled(sc.rel, detail)
	return nil
}

// snapshotModeBits are the only mode bits contextopt's snapshot writers accept: they write
// text files, never an executable one.
const snapshotModeBits os.FileMode = 0o644

// publishPriorRefresh writes content over actual at full. A text scaffold is replaced bound to
// actual, so a file edited in between is not overwritten; a confined one goes through the
// root-pinned writer compile-context uses (contextopt.ReplaceSnapshotIn), so a symlink at or
// below the repository is refused rather than written through. An executable scaffold, such as
// the interceptor, is written the way it is created and replaced under --force (writeRepoFile),
// without that binding: the snapshot writers set no execute bit.
func (s *adoptSession) publishPriorRefresh(ctx context.Context, full string, actual, content []byte, sc scaffold) error {
	options := contextopt.ReplaceOptions{Expected: actual, Exists: true, Mode: sc.perm}
	switch {
	case sc.confined:
		return contextopt.ReplaceSnapshotIn(ctx, s.repoPath, filepath.FromSlash(sc.rel), content, options)
	case sc.perm&^snapshotModeBits != 0:
		return writeRepoFile(full, content, sc.perm)
	}
	return contextopt.ReplaceSnapshot(ctx, full, content, options)
}

// writeScaffold persists sc at full unless the session is a dry run, through the root-pinned
// writer when sc is confined.
func (s *adoptSession) writeScaffold(ctx context.Context, full string, sc scaffold) error {
	if !sc.confined || s.opts.DryRun {
		return s.write(full, sc.content, sc.perm)
	}
	if err := contextopt.WriteSnapshotIn(ctx, s.repoPath, filepath.FromSlash(sc.rel), sc.content, sc.perm); err != nil {
		return fmt.Errorf("write %s: %w", sc.rel, err)
	}
	return nil
}

// recordExistingScaffold classifies an existing file against its scaffold and records the
// result. A file that cannot be read is preserved and reported as unverified: a file that
// exists is not evidence of anything until its content has been compared, and there are no
// bytes to back up. A drifted file is replaced when Force is set and audit locks the scaffold
// (auditLocked), a file with mixed line endings included, since it cannot hold the scaffold's
// text, and preserved otherwise.
func (s *adoptSession) recordExistingScaffold(ctx context.Context, full string, sc scaffold) (scaffoldState, error) {
	actual, _, readErr := contextopt.ObserveSnapshot(ctx, full)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	identical, err := false, readErr
	if readErr == nil {
		identical, err = util.CanonicalTextEquivalent(actual, sc.content)
	}
	switch {
	case readErr == nil && !identical && sc.auditLocked && s.opts.Force:
		return s.replaceScaffold(ctx, full, sc, actual)
	case err != nil:
		s.report.recordReconciled(sc.rel, "Existing file preserved unverified: "+err.Error())
		s.report.addWarning("%s: existing file preserved but not compared with the scaffold: %v", sc.rel, err)
		return scaffoldUnverified, nil
	case identical:
		s.report.recordReconciled(sc.rel, sc.verified)
		return scaffoldIdentical, nil
	}
	note := scaffoldDriftNote(sc.auditLocked, actual, sc.content)
	s.report.recordReconciled(sc.rel, note)
	s.report.addWarning("%s: %s", sc.rel, lowerFirst(note))
	return scaffoldDrifted, nil
}

// replaceScaffold overwrites the drifted file at full, which held actual, with sc.content under
// --force, through the writer that creates the scaffold.
func (s *adoptSession) replaceScaffold(ctx context.Context, full string, sc scaffold, actual []byte) (scaffoldState, error) {
	err := s.replaceExisting(ctx, replacement{
		rel: sc.rel, before: actual, after: sc.content, detail: sc.created,
		publish: func(ctx context.Context) error { return s.writeScaffold(ctx, full, sc) },
	})
	if err != nil {
		return 0, err
	}
	return scaffoldWritten, nil
}

// scaffoldDriftNote says what an operator can do about a drifted file, which holds actual where
// the scaffold writes content: --force regenerates only a file audit locks (auditLocked). Every
// other file is not audit-verified and stays, --force included; the note counts the lines
// regenerating it would remove and add (lineDeltaCounts) and says how to regenerate it.
func scaffoldDriftNote(auditLocked bool, actual, content []byte) string {
	if auditLocked {
		return "Existing file differs from the scaffold adoption writes; preserved, not verified (--force regenerates it)"
	}
	counts := lineDeltaCounts(util.LineDeltaOf(string(actual), string(content), 0))
	return "Existing file differs from the scaffold adoption writes (" + counts + "); not audit-verified; kept, " +
		"--force included (delete it and re-run adopt to regenerate it)"
}
