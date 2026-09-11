package state

import (
	"fmt"
	"path/filepath"
	"strings"

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

	requiredFiles := []string{"STATE.md", "OPEN.md", "BACKLOG.md", "BUGS.md", "QUESTIONS.md"}
	for _, f := range requiredFiles {
		target := filepath.Join(wDir, f)
		if !util.FileExists(target) {
			rep.Valid = false
			rep.MissingFiles = append(rep.MissingFiles, f)
			rep.Violations = append(rep.Violations, fmt.Sprintf("missing required working directory file: %s", f))
		}
	}

	allBugs, _ := ListBugs(rootPath, "all")
	rep.TotalBugs = len(allBugs)
	for _, b := range allBugs {
		if strings.EqualFold(b.Status, "open") || strings.EqualFold(b.Status, "investigating") {
			rep.OpenBugs++
			if strings.EqualFold(b.Severity, "p0") {
				rep.P0Bugs++
				rep.Violations = append(rep.Violations, fmt.Sprintf("unresolved P0 blocker in bug ledger: %s (%s)", b.ID, b.Title))
			}
		}
	}

	allQs, _ := ListQuestions(rootPath, "pending")
	rep.PendingQuestions = len(allQs)

	if len(rep.Violations) > 0 {
		rep.Valid = false
	}
	return rep, nil
}
