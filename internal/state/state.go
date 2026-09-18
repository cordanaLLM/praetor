package state

import (
	"context"
	"fmt"
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
	GitState       string    `json:"git_state"`
	Clean          bool      `json:"clean"`
	DirtyCount     int       `json:"dirty_count"`
	OpenTasks      int       `json:"open_tasks"`
	CompletedTasks int       `json:"completed_tasks"`
	OpenBugs       int       `json:"open_bugs"`
	PendingQs      int       `json:"pending_questions"`
	LastUpdated    time.Time `json:"last_updated"`
	StateHash      string    `json:"state_hash,omitempty"`
}

// SyncState captures git HEAD and updates STATE.md with a session activity entry.
func SyncState(ctx context.Context, rootPath string, sessionSummary string) (*StateSnapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("state sync requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := InitWorkingDirIfAbsentContext(ctx, rootPath); err != nil {
		return nil, err
	}

	snap, err := InspectState(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap.StateHash, err = stateBinding(ctx, rootPath, snap)
	if err != nil {
		return nil, err
	}
	if err := appendStateLog(ctx, rootPath, snap, sessionSummary); err != nil {
		return snap, err
	}
	return snap, VerifyStateSync(ctx, rootPath)
}

// InspectState reads Git and ledger state without initialization or log writes.
// Roots outside a worktree are explicitly marked not_repository; failures in an
// existing worktree are errors rather than fabricated clean snapshots.
func InspectState(ctx context.Context, rootPath string) (*StateSnapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("state inspection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap := &StateSnapshot{RepoPath: rootPath, LastUpdated: time.Now().UTC()}
	if err := populateGitSnapshot(ctx, rootPath, snap); err != nil {
		return nil, err
	}
	if err := populateLedgerSnapshot(ctx, rootPath, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func populateGitSnapshot(ctx context.Context, rootPath string, snap *StateSnapshot) error {
	present, err := util.GitWorktreePresent(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("inspect state Git worktree: %w", err)
	}
	if !present {
		snap.GitState = "not_repository"
		snap.Branch, snap.HeadSHA = "(not a git worktree)", "(unavailable)"
		return nil
	}
	if err := rejectStateGitFilters(ctx, rootPath); err != nil {
		return err
	}
	snap.Branch, err = stateGitString(ctx, rootPath, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("read state Git branch: %w", err)
	}
	snap.HeadSHA, err = stateGitHead(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("read state Git HEAD: %w", err)
	}
	status, err := stateGitString(ctx, rootPath, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=all", "--", ".", ":(top,exclude).workingdir")
	if err != nil {
		return fmt.Errorf("read state Git status: %w", err)
	}
	snap.GitState = "available"
	if snap.HeadSHA == "(unborn)" {
		snap.GitState = "unborn"
	}
	snap.Clean = status == ""
	if !snap.Clean {
		snap.DirtyCount = len(strings.Split(status, "\n"))
	}
	return nil
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
	_, err = writeStateEntry(ctx, stateFile, content, supersededBase(content), snap, summary)
	return err
}

// supersededBase drops the trailing sync marker. Only the last marker is ever
// verified, and the marker written with the new entry supersedes it.
func supersededBase(content []byte) string {
	if match := syncMarker.FindIndex(content); match != nil {
		return string(content[:match[0]])
	}
	return string(content)
}

// writeStateEntry appends one compact entry and its sync marker to base and
// replaces STATE.md, provided the file still holds expected. snap.StateHash
// carries the binding in and the marker hash out. It returns the bytes written.
func writeStateEntry(ctx context.Context, stateFile string, expected []byte, base string, snap *StateSnapshot, summary string) (int, error) {
	updated := base + "\n" + entryFromSnapshot(snap, summary).render()
	snap.StateHash = stateLogHash(snap.StateHash, []byte(updated))
	updated += "\n<!-- praetor-state:v1 sha256:" + snap.StateHash + " -->\n"

	if len(updated) > contextopt.MaxSourceBytes {
		return 0, fmt.Errorf("STATE.md append exceeds %d bytes", contextopt.MaxSourceBytes)
	}
	options := contextopt.ReplaceOptions{Expected: expected, Exists: true, Mode: 0o600}
	return len(updated), contextopt.ReplaceSnapshot(ctx, stateFile, []byte(updated), options)
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
