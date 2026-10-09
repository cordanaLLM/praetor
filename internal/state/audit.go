package state

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// StateAuditReport details the integrity and hygiene of the .workingdir state.
type StateAuditReport struct {
	Valid            bool     `json:"valid"`
	WorkingDirExists bool     `json:"working_dir_exists"`
	TotalBugs        int      `json:"total_bugs"`
	OpenBugs         int      `json:"open_bugs"`
	P0Bugs           int      `json:"p0_bugs"`
	PendingQuestions int      `json:"pending_questions"`
	MissingFiles     []string `json:"missing_files,omitempty"`
	Violations       []string `json:"violations,omitempty"`
}

// AuditWorkingDir validates .workingdir completeness, bug ledger health, and question hygiene.
func AuditWorkingDir(rootPath string) (*StateAuditReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return AuditWorkingDirContext(ctx, rootPath)
}

// AuditWorkingDirContext is AuditWorkingDir under the caller's cancellation.
func AuditWorkingDirContext(ctx context.Context, rootPath string) (*StateAuditReport, error) {
	wDir := filepath.Join(rootPath, WorkingDirName)
	rep := &StateAuditReport{
		Valid:            true,
		WorkingDirExists: util.DirExists(wDir),
		MissingFiles:     make([]string, 0),
		Violations:       make([]string, 0),
	}

	if !rep.WorkingDirExists {
		rep.Valid = false
		rep.Violations = append(rep.Violations, fmt.Sprintf("working directory %s does not exist", WorkingDirName))
		return rep, nil
	}

	for _, f := range ledgerFileNames() {
		target := filepath.Join(wDir, f)
		exists, err := ledgerFileExists(target)
		if err != nil {
			rep.Valid = false
			return rep, fmt.Errorf("audit ledger %s: %w", f, err)
		}
		if !exists {
			rep.Valid = false
			rep.MissingFiles = append(rep.MissingFiles, f)
			rep.Violations = append(rep.Violations, fmt.Sprintf("missing required working directory file: %s", f))
		}
	}

	allBugs, err := ListBugsContext(ctx, rootPath, "all")
	if err != nil {
		rep.Valid = false
		return rep, fmt.Errorf("audit list bugs: %w", err)
	}
	rep.TotalBugs = len(allBugs)
	countOpenAuditBugs(rep, allBugs)

	allQs, err := ListQuestionsContext(ctx, rootPath, "pending")
	if err != nil {
		rep.Valid = false
		return rep, fmt.Errorf("audit list questions: %w", err)
	}
	rep.PendingQuestions = len(allQs)

	if len(rep.Violations) > 0 {
		rep.Valid = false
	}
	return rep, nil
}

func countOpenAuditBugs(rep *StateAuditReport, bugs []BugEntry) {
	for _, b := range bugs {
		if b.Unresolved() {
			rep.OpenBugs++
			if strings.EqualFold(b.Severity, "p0") {
				rep.P0Bugs++
				rep.Violations = append(rep.Violations, fmt.Sprintf("unresolved P0 blocker in bug ledger: %s (%s)", b.ID, b.Title))
			}
		}
	}
}
