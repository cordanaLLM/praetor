package adopt

import (
	"fmt"
	"os"
)

// Action values recorded in ActionDetail.
const (
	actionCreate    = "create"
	actionReconcile = "reconcile"
	actionMerge     = "merge"
	actionAppend    = "append"
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
	r.ActionDetails = append(r.ActionDetails, ActionDetail{Path: path, Action: actionSkip, Details: details})
	r.Warnings = append(r.Warnings, path+": "+details)
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
	verified string      // action detail when an existing file is left in place
}

// write persists data unless the session is a dry run.
func (s *adoptSession) write(path string, data []byte, perm os.FileMode) error {
	if s.opts.DryRun {
		return nil
	}
	return writeRepoFile(path, data, perm)
}

// scaffoldFile creates sc.rel when it is missing (or when Force is set and the scaffold
// allows overwriting). It reports whether the file was written or planned by this run.
func (s *adoptSession) scaffoldFile(sc scaffold) (bool, error) {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return false, err
	}
	if fileExists(full) && (!sc.force || !s.opts.Force) {
		s.report.recordReconciled(sc.rel, sc.verified)
		return false, nil
	}
	if err := s.write(full, sc.content, sc.perm); err != nil {
		return false, err
	}
	s.report.recordCreated(sc.rel, sc.created)
	return true, nil
}
