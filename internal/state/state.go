package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// WorkingDirName is the canonical working directory identifier.
const WorkingDirName = ".workingdir"

// StateSnapshot captures the active git and working directory state.
type StateSnapshot struct {
	RepoPath       string    `json:"repo_path"`
	Branch         string    `json:"branch"`
	HeadSHA        string    `json:"head_sha"`
	Clean          bool      `json:"clean"`
	DirtyCount     int       `json:"dirty_count"`
	OpenTasks      int       `json:"open_tasks"`
	CompletedTasks int       `json:"completed_tasks"`
	OpenBugs       int       `json:"open_bugs"`
	PendingQs      int       `json:"pending_questions"`
	LastUpdated    time.Time `json:"last_updated"`
}

// InitWorkingDir scaffolds the canonical .workingdir directory lattice if missing.
func InitWorkingDir(rootPath string) error {
	wDir := filepath.Join(rootPath, WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", wDir, err)
	}
	if err := os.MkdirAll(filepath.Join(wDir, "evidence"), 0755); err != nil {
		return fmt.Errorf("failed to create evidence dir: %w", err)
	}

	files := map[string]string{
		"STATE.md":     defaultStateMD(),
		"OPEN.md":      defaultOpenMD(),
		"BACKLOG.md":   defaultBacklogMD(),
		"BUGS.md":      defaultBugsMD(),
		"QUESTIONS.md": defaultQuestionsMD(),
	}

	for rel, content := range files {
		target := filepath.Join(wDir, rel)
		if !util.FileExists(target) {
			if err := os.WriteFile(target, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", rel, err)
			}
		}
	}
	return nil
}

// SyncState captures git HEAD and updates STATE.md with a session activity entry.
func SyncState(ctx context.Context, rootPath string, sessionSummary string) (*StateSnapshot, error) {
	if err := InitWorkingDir(rootPath); err != nil {
		return nil, err
	}

	snap := &StateSnapshot{
		RepoPath:    rootPath,
		LastUpdated: time.Now().UTC(),
	}

	branch, _ := util.RunGit(ctx, rootPath, "branch", "--show-current")
	snap.Branch = branch
	head, _ := util.RunGit(ctx, rootPath, "rev-parse", "--short", "HEAD")
	snap.HeadSHA = head

	statusOut, _ := util.RunGit(ctx, rootPath, "status", "--porcelain")
	if strings.TrimSpace(statusOut) == "" {
		snap.Clean = true
	} else {
		snap.Clean = false
		snap.DirtyCount = len(strings.Split(strings.TrimSpace(statusOut), "\n"))
	}

	tasks, _ := ListTasks(rootPath)
	openTasks := 0
	doneTasks := 0
	for _, t := range tasks {
		if t.Completed {
			doneTasks++
		} else {
			openTasks++
		}
	}
	snap.OpenTasks = openTasks
	snap.CompletedTasks = doneTasks

	bugs, _ := ListBugs(rootPath, "open")
	snap.OpenBugs = len(bugs)

	qs, _ := ListQuestions(rootPath, "pending")
	snap.PendingQs = len(qs)

	if err := appendStateLog(rootPath, snap, sessionSummary); err != nil {
		return snap, err
	}

	return snap, nil
}

func appendStateLog(rootPath string, snap *StateSnapshot, summary string) error {
	stateFile := filepath.Join(rootPath, WorkingDirName, "STATE.md")
	content, err := os.ReadFile(stateFile)
	if err != nil {
		content = []byte(defaultStateMD())
	}

	timeStr := snap.LastUpdated.Format("2006-01-02 15:04:05 UTC")
	logMsg := summary
	if logMsg == "" {
		logMsg = "Automated state synchronization"
	}

	entry := fmt.Sprintf("\n### [%s] Commit `%s` on `%s`\n- **Activity**: %s\n- **Tasks**: %d open, %d completed | **Open Bugs**: %d | **Pending Questions**: %d\n",
		timeStr, snap.HeadSHA, snap.Branch, logMsg, snap.OpenTasks, snap.CompletedTasks, snap.OpenBugs, snap.PendingQs)

	updated := string(content) + entry
	return os.WriteFile(stateFile, []byte(updated), 0644)
}

func defaultStateMD() string {
	return `# Session State & Active Working Context

> Persistent local session state across workstations and AI agents.
> Auto-updated via praetorctl state sync and Git post-commit hooks.

## Active Session Overview
`
}

func defaultOpenMD() string {
	return `# Open Items & In-Flight Blockers

> Track active, immediate operational blockers and current research spikes.

## In-Flight Tasks
- [ ] Task 1: Ongoing execution
`
}

func defaultBacklogMD() string {
	return `# Project Backlog & Longer-Horizon Workstreams

> Long-horizon architectural goals, deferred feature requests, and discharged milestones.

## Discharged Milestones
- [x] Initial Praetor Governance Adoption

## Future Workstreams
- [ ] Deep AST Deduplication Sweeps
`
}
