package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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

// SyncState captures git HEAD and updates STATE.md with a session activity entry.
func SyncState(ctx context.Context, rootPath string, sessionSummary string) (*StateSnapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("state sync requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := initWorkingDir(ctx, rootPath); err != nil {
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

	if err := populateLedgerSnapshot(ctx, rootPath, snap); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := appendStateLog(ctx, rootPath, snap, sessionSummary); err != nil {
		return snap, err
	}
	return snap, nil
}

func populateLedgerSnapshot(ctx context.Context, rootPath string, snap *StateSnapshot) error {
	tasks, err := ListTasksContext(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("state sync list tasks: %w", err)
	}
	for _, t := range tasks {
		if t.Completed {
			snap.CompletedTasks++
		} else {
			snap.OpenTasks++
		}
	}

	bugs, err := ListBugsContext(ctx, rootPath, "open")
	if err != nil {
		return fmt.Errorf("state sync list bugs: %w", err)
	}
	snap.OpenBugs = len(bugs)

	qs, err := ListQuestionsContext(ctx, rootPath, "pending")
	if err != nil {
		return fmt.Errorf("state sync list questions: %w", err)
	}
	snap.PendingQs = len(qs)
	return nil
}

func appendStateLog(ctx context.Context, rootPath string, snap *StateSnapshot, summary string) error {
	stateFile := filepath.Join(rootPath, WorkingDirName, "STATE.md")
	content, err := contextopt.ReadSnapshot(ctx, stateFile)
	if err != nil {
		return fmt.Errorf("read STATE.md before append: %w", err)
	}

	timeStr := snap.LastUpdated.Format("2006-01-02 15:04:05 UTC")
	logMsg := summary
	if logMsg == "" {
		logMsg = "Automated state synchronization"
	}

	entry := fmt.Sprintf("\n### [%s] Commit `%s` on `%s`\n- **Activity**: %s\n- **Tasks**: %d open, %d completed | **Open Bugs**: %d | **Pending Questions**: %d\n",
		timeStr, snap.HeadSHA, snap.Branch, logMsg, snap.OpenTasks, snap.CompletedTasks, snap.OpenBugs, snap.PendingQs)

	if len(content)+len(entry) > contextopt.MaxSourceBytes {
		return fmt.Errorf("STATE.md append exceeds %d bytes", contextopt.MaxSourceBytes)
	}
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
