package adopt

import (
	"context"
	"fmt"
	"os"

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
		return s.recordExistingScaffold(ctx, full, sc)
	}
	if err := s.write(full, sc.content, sc.perm); err != nil {
		return 0, err
	}
	s.report.recordCreated(sc.rel, sc.created)
	return scaffoldWritten, nil
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
