package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Invariant limits enforcing HISS-02 (bounded execution) and HISS-04 (complexity bounds).
const (
	DefaultGitTimeout = 30 * time.Second
	MaxPorcelainLines = 50000
	MaxTaskIDLength   = 128
	WorktreeSubdir    = ".standards/worktrees"
	BranchPrefix      = "wt/"
)

var (
	ErrNilContext        = errors.New("context cannot be nil")
	ErrNilManager        = errors.New("manager cannot be nil")
	ErrEmptyTaskID       = errors.New("taskID cannot be empty")
	ErrTaskIDTooLong     = errors.New("taskID exceeds maximum length")
	ErrInvalidTaskID     = errors.New("taskID contains invalid characters")
	ErrEmptyBaseBranch   = errors.New("baseBranch cannot be empty")
	ErrInvalidBaseBranch = errors.New("baseBranch contains invalid characters")
	ErrLimitExceeded     = errors.New("line count exceeds limit")
	validTaskIDRegex     = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// Worktree represents an ephemeral isolated git worktree workspace.
type Worktree struct {
	TaskID     string `json:"task_id"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// WorktreeInfo holds metadata for a single git worktree parsed from porcelain output.
type WorktreeInfo struct {
	Path        string `json:"path"`
	HEAD        string `json:"head"`
	Branch      string `json:"branch"`
	Ref         string `json:"ref,omitempty"`
	Bare        bool   `json:"bare,omitempty"`
	Detached    bool   `json:"detached,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
	LockReason  string `json:"lock_reason,omitempty"`
	Prunable    bool   `json:"prunable,omitempty"`
	PruneReason string `json:"prune_reason,omitempty"`
}

// Manager manages ephemeral git worktrees under .standards/worktrees/<task-id>.
type Manager struct {
	rootDir string
	mu      sync.Mutex
}

// NewManager constructs a Manager instance rooted at the specified repository directory.
func NewManager(rootDir string) *Manager {
	clean := filepath.Clean(rootDir)
	if clean == "" {
		clean = "."
	}
	return &Manager{
		rootDir: clean,
	}
}

// RootDir returns the root repository path.
func (m *Manager) RootDir() string {
	if m == nil {
		return ""
	}
	return m.rootDir
}

// WorktreePath returns the target filesystem path for a given taskID.
func (m *Manager) WorktreePath(taskID string) string {
	if m == nil {
		return filepath.Join(filepath.FromSlash(WorktreeSubdir), taskID)
	}
	return filepath.Join(m.rootDir, filepath.FromSlash(WorktreeSubdir), taskID)
}

// Create executes 'git worktree add -b wt/<taskID> <path> <baseBranch>' and returns the created Worktree.
func (m *Manager) Create(ctx context.Context, taskID, baseBranch string) (*Worktree, error) {
	if m == nil {
		return nil, ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateTaskID(taskID); err != nil {
		return nil, fmt.Errorf("invalid taskID for create: %w", err)
	}
	if err := validateBaseBranch(baseBranch); err != nil {
		return nil, fmt.Errorf("invalid baseBranch for create: %w", err)
	}

	wtPath := m.WorktreePath(taskID)
	parentDir := filepath.Dir(wtPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating parent directory %s: %w", parentDir, err)
	}

	branch := BranchPrefix + taskID
	args := []string{"worktree", "add", "-b", branch, wtPath, baseBranch}
	if _, err := m.runGit(ctx, args...); err != nil {
		return nil, fmt.Errorf("failed creating worktree for task %s: %w", taskID, err)
	}

	return &Worktree{
		TaskID:     taskID,
		Branch:     branch,
		Path:       wtPath,
		BaseBranch: baseBranch,
	}, nil
}

// Remove executes 'git worktree remove [--force] <path>' and 'git branch -D wt/<taskID>'.
func (m *Manager) Remove(ctx context.Context, taskID string, force bool) error {
	if m == nil {
		return ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateTaskID(taskID); err != nil {
		return fmt.Errorf("invalid taskID for remove: %w", err)
	}

	wtPath := m.WorktreePath(taskID)
	branch := BranchPrefix + taskID

	removeArgs := []string{"worktree", "remove"}
	if force {
		removeArgs = append(removeArgs, "--force")
	}
	removeArgs = append(removeArgs, wtPath)

	if _, err := m.runGit(ctx, removeArgs...); err != nil {
		return fmt.Errorf("failed removing worktree at %s: %w", wtPath, err)
	}

	branchArgs := []string{"branch", "-D", branch}
	if _, err := m.runGit(ctx, branchArgs...); err != nil {
		return fmt.Errorf("failed deleting branch %s: %w", branch, err)
	}

	return nil
}

// List executes 'git worktree list --porcelain' and parses its output into a WorktreeInfo slice.
func (m *Manager) List(ctx context.Context) ([]WorktreeInfo, error) {
	if m == nil {
		return nil, ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out, err := m.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed listing worktrees: %w", err)
	}

	return parseWorktreeList(string(out))
}

// Prune executes 'git worktree prune' to clean up stale worktree administrative files.
func (m *Manager) Prune(ctx context.Context) error {
	if m == nil {
		return ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.runGit(ctx, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed pruning worktrees: %w", err)
	}
	return nil
}

func (m *Manager) runGit(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before git execution: %w", err)
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		execCtx, cancel = context.WithTimeout(ctx, DefaultGitTimeout)
		defer cancel()
	}

	gitArgs := append([]string{"-c", "core.longpaths=true"}, args...)
	cmd := exec.CommandContext(execCtx, "git", gitArgs...)
	cmd.Dir = m.rootDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func validateTaskID(taskID string) error {
	if taskID == "" {
		return ErrEmptyTaskID
	}
	if len(taskID) > MaxTaskIDLength {
		return fmt.Errorf("%w: length %d > %d", ErrTaskIDTooLong, len(taskID), MaxTaskIDLength)
	}
	if !validTaskIDRegex.MatchString(taskID) {
		return fmt.Errorf("%w: %q (must match [a-zA-Z0-9_-]+)", ErrInvalidTaskID, taskID)
	}
	return nil
}

func validateBaseBranch(baseBranch string) error {
	trimmed := strings.TrimSpace(baseBranch)
	if trimmed == "" {
		return ErrEmptyBaseBranch
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, " ~^:?*[\x00-\x1f\\") {
		return fmt.Errorf("%w: %q", ErrInvalidBaseBranch, baseBranch)
	}
	return nil
}

func parseWorktreeList(output string) ([]WorktreeInfo, error) {
	lines := strings.Split(output, "\n")
	lineCount := len(lines)
	if lineCount > MaxPorcelainLines {
		return nil, fmt.Errorf("%w: line count %d > %d", ErrLimitExceeded, lineCount, MaxPorcelainLines)
	}

	var results []WorktreeInfo
	var current WorktreeInfo
	hasCurrent := false

	for i := 0; i < lineCount && i < MaxPorcelainLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			if hasCurrent {
				results = append(results, current)
				current = WorktreeInfo{}
				hasCurrent = false
			}
			continue
		}

		hasCurrent = true
		parseAttributeLine(line, &current)
	}

	if hasCurrent {
		results = append(results, current)
	}

	return results, nil
}

func parseAttributeLine(line string, info *WorktreeInfo) {
	parts := strings.SplitN(line, " ", 2)
	key := parts[0]
	val := ""
	if len(parts) > 1 {
		val = parts[1]
	}

	switch key {
	case "worktree":
		info.Path = val
	case "HEAD":
		info.HEAD = val
	case "branch":
		info.Ref = val
		info.Branch = strings.TrimPrefix(val, "refs/heads/")
	case "bare":
		info.Bare = true
	case "detached":
		info.Detached = true
	case "locked":
		info.Locked = true
		info.LockReason = val
	case "prunable":
		info.Prunable = true
		info.PruneReason = val
	}
}
