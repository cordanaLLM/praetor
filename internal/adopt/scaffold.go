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
	rel      string      // repository-relative path
	perm     os.FileMode // file mode
	content  []byte      // payload written when the file is created
	force    bool        // whether AdoptOptions.Force may overwrite an existing file
	created  string      // action detail when the file is (or would be) written
	verified string      // action detail when an existing file matches content
	// confined writes through contextopt.WriteSnapshotIn, the writer compile-context uses, which
	// follows no symlink below the repository. It is set for the files compile-context also
	// reads (the canonical personas); every other scaffold goes through writeRepoFile.
	confined bool
	// prior holds the digest (priorRendering) of every text an earlier Praetor wrote at rel,
	// keyed to what produced it. An existing file holding one of them, in one consistent
	// line-ending style, is Praetor output nobody edited, so adoption refreshes it to content
	// in that style without --force, as it migrates an earlier lefthook.yml
	// (isPriorLefthookConfig). An edited copy matches no digest and keeps the force contract.
	// A confined scaffold ignores it: the refresh does not go through the root-pinned writer.
	prior map[string]string
	// refreshed is the action detail when an earlier Praetor text is refreshed.
	refreshed string
}

// priorRendering reports whether data is one of the earlier Praetor texts digests names, and
// whether data is its CRLF checkout. It is the one rule every earlier-text set in adoption
// (labels, lefthook.yml, the pinned catalog) is read with: a key is util.CanonicalTextDigest of
// the recorded LF text, so a checkout that converted it to CRLF (core.autocrlf on Windows) is
// still recognised, while an edit, a lost final newline or mixed line endings match nothing
// (HISS-21).
func priorRendering(data []byte, digests map[string]string) (known, crlf bool) {
	digest, crlf, err := util.CanonicalTextDigest(data)
	if err != nil {
		return false, false
	}
	_, known = digests[digest]
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
	// scaffoldWritten: the file was created, or regenerated under --force (planned in a dry run).
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

// scaffoldFile creates sc.rel when it is missing (or when Force is set and the scaffold
// allows overwriting). An existing file it leaves in place is compared with sc.content
// first: only a match is reported as sc.verified, and a difference is reported as drift
// rather than overwritten or passed off as verified.
func (s *adoptSession) scaffoldFile(ctx context.Context, sc scaffold) (scaffoldState, error) {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return 0, err
	}
	if fileExists(full) && (!sc.force || !s.opts.Force) {
		refreshed, err := s.refreshPriorScaffold(ctx, full, sc)
		if err != nil || refreshed {
			return scaffoldWritten, err
		}
		return s.recordExistingScaffold(ctx, full, sc)
	}
	if err := s.writeScaffold(ctx, full, sc); err != nil {
		return 0, err
	}
	s.report.recordCreated(sc.rel, sc.created)
	return scaffoldWritten, nil
}

// refreshPriorScaffold replaces an existing file holding an earlier Praetor text of sc with
// sc.content in the file's own line-ending style, bound to the bytes it observed, and reports
// whether it did. A dry run reports the refresh it would make.
func (s *adoptSession) refreshPriorScaffold(ctx context.Context, full string, sc scaffold) (bool, error) {
	if len(sc.prior) == 0 || sc.confined {
		return false, nil
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists {
		return false, err
	}
	known, crlf := priorRendering(actual, sc.prior)
	if !known {
		return false, nil
	}
	if !s.opts.DryRun {
		content := []byte(util.RestoreLineEndings(string(sc.content), crlf))
		if err := contextopt.ReplaceSnapshot(ctx, full, content, contextopt.ReplaceOptions{
			Expected: actual, Exists: true, Mode: sc.perm,
		}); err != nil {
			return false, fmt.Errorf("refresh %s: %w", sc.rel, err)
		}
	}
	s.report.recordReconciled(sc.rel, sc.refreshed)
	return true, nil
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

// recordExistingScaffold classifies a preserved file against its scaffold and records the
// result. A file that cannot be read is preserved and reported as unverified: a file that
// exists is not evidence of anything until its content has been compared.
func (s *adoptSession) recordExistingScaffold(ctx context.Context, full string, sc scaffold) (scaffoldState, error) {
	actual, _, err := contextopt.ObserveSnapshot(ctx, full)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	identical := false
	if err == nil {
		identical, err = util.CanonicalTextEquivalent(actual, sc.content)
	}
	switch {
	case err != nil:
		s.report.recordReconciled(sc.rel, "Existing file preserved unverified: "+err.Error())
		s.report.addWarning("%s: existing file preserved but not compared with the scaffold: %v", sc.rel, err)
		return scaffoldUnverified, nil
	case identical:
		s.report.recordReconciled(sc.rel, sc.verified)
		return scaffoldIdentical, nil
	}
	note := scaffoldDriftNote(sc.force)
	s.report.recordReconciled(sc.rel, note)
	s.report.addWarning("%s: %s", sc.rel, lowerFirst(note))
	return scaffoldDrifted, nil
}

// scaffoldDriftNote says what an operator can do about a drifted file: --force regenerates
// only the scaffolds that allow it, and every other one is the operator's to reconcile.
func scaffoldDriftNote(forceable bool) string {
	if forceable {
		return "Existing file differs from the scaffold adoption writes; preserved, not verified (--force regenerates it)"
	}
	return "Existing file differs from the scaffold adoption writes; preserved, not verified"
}
