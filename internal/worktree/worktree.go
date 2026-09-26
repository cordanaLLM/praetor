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

	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant limits enforcing HISS-02 (bounded execution) and HISS-04 (complexity bounds).
const (
	DefaultGitTimeout = 30 * time.Second
	MaxPorcelainLines = 50000
	MaxTaskIDLength   = 128
	WorktreeSubdir    = ".standards/worktrees"
	BranchPrefix      = "wt/"
	// worktreeDirPerm is the mode of the worktree container directory.
	worktreeDirPerm os.FileMode = 0o755
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

const removalProbeMaxBytes = 1 << 20

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
//
// The root is made absolute here. Git runs with the root as its working directory and is
// handed the worktree path as an argument, so a relative root such as "repo" used to be
// applied twice: git created repo/repo/.standards/worktrees/<id> while the returned Path
// named repo/.standards/worktrees/<id>. An empty root means the current directory. Should
// the current directory be unresolvable, the cleaned root is kept as given.
func NewManager(rootDir string) *Manager {
	clean := filepath.Clean(rootDir)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
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
	if err := util.MkdirSecure(parentDir, worktreeDirPerm); err != nil {
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

// Remove removes the task's worktree. Safe removal (force false) runs the released-removal
// checks and 'git worktree remove <path>', and preserves the branch so unpublished commits
// remain reachable. Forced removal runs 'git worktree remove --force --force <path>' and
// deletes the managed branch with 'git branch -D'; see forceRemoveUnlocked.
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
	if !force {
		return m.removeReleasedUnlocked(ctx, wtPath)
	}
	return m.forceRemoveUnlocked(ctx, wtPath, BranchPrefix+taskID)
}

// forceRemoveUnlocked discards a task's worktree and its managed branch.
//
// --force is given twice because git removes a locked worktree only then (git-worktree(1):
// "To remove a locked worktree, specify --force twice"); a single --force refused exactly
// the worktree the CLI help promises to remove.
//
// The branch is deleted even when the worktree removal fails. A worktree whose directory
// and administrative entry are already gone fails 'git worktree remove', and returning at
// that point left wt/<id> behind with nothing that would ever delete it. Deleting a branch
// still checked out in a registered worktree is refused by git itself, so trying cannot
// discard a worktree that survived. Both failures are reported.
func (m *Manager) forceRemoveUnlocked(ctx context.Context, wtPath, branch string) error {
	var removeErr error
	if _, err := m.runGit(ctx, "worktree", "remove", "--force", "--force", wtPath); err != nil {
		removeErr = fmt.Errorf("failed removing worktree at %s: %w", wtPath, err)
	}
	if _, err := m.runGit(ctx, "branch", "-D", branch); err != nil {
		if removeErr != nil {
			return errors.Join(removeErr, fmt.Errorf("failed deleting branch %s: %w", branch, err))
		}
		return fmt.Errorf("worktree removed at %s, but failed deleting branch %s: %w", wtPath, branch, err)
	}
	return removeErr
}

// CheckRemoval verifies that path is a registered, clean linked worktree owned by
// this manager. It performs no mutation and refuses ambiguous Git state.
func (m *Manager) CheckRemoval(ctx context.Context, path string) error {
	if m == nil {
		return ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkRemovalUnlocked(ctx, path)
}

// RemoveReleased removes a previously released worktree without force and keeps
// its branch. It repeats registration and cleanliness checks immediately before
// removal; callers must keep the path quiescent during this operation.
func (m *Manager) RemoveReleased(ctx context.Context, path string) error {
	if m == nil {
		return ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeReleasedUnlocked(ctx, path)
}

func (m *Manager) removeReleasedUnlocked(ctx context.Context, path string) error {
	targetPath, err := m.validatedRemovalPathUnlocked(ctx, path)
	if err != nil {
		return err
	}
	if _, err := m.runGit(ctx, "-c", "core.fsmonitor=false", "worktree", "remove", targetPath); err != nil {
		return fmt.Errorf("failed removing released worktree at %s: %w", path, err)
	}
	return nil
}

func (m *Manager) checkRemovalUnlocked(ctx context.Context, path string) error {
	_, err := m.validatedRemovalPathUnlocked(ctx, path)
	return err
}

func (m *Manager) validatedRemovalPathUnlocked(ctx context.Context, path string) (string, error) {
	targetPath, err := inspectRemovalPath(ctx, path)
	if err != nil {
		return "", err
	}
	match, rootPath, err := m.findRegisteredRemoval(ctx, path, targetPath)
	if err != nil {
		return "", err
	}
	if err := validateRegisteredRemoval(path, targetPath, rootPath, match); err != nil {
		return "", err
	}
	if err := m.checkRemovalFilters(ctx, targetPath); err != nil {
		return "", err
	}
	if err := m.checkRemovalGitlinks(ctx, targetPath); err != nil {
		return "", err
	}
	statusResult, statusErr := util.RunGitProbe(ctx, targetPath, removalProbeMaxBytes, "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if statusErr != nil {
		return "", fmt.Errorf("cannot inspect worktree cleanliness at %s: %w", path, statusErr)
	}
	status := string(statusResult.Stdout)
	if strings.TrimSpace(status) == "" {
		return targetPath, nil
	}
	if removalStatusHasOnlyIgnored(status) {
		return "", fmt.Errorf("refusing removal of worktree %s: ignored files are present", path)
	}
	return "", fmt.Errorf("refusing removal of worktree %s: changes or untracked files are present", path)
}

func inspectRemovalPath(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context error before removal check: %w", err)
	}
	targetPath, err := canonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("cannot inspect removal path %s: %w", path, err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("removal path %s is unavailable: %w", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing removal path %s: final path component is a symbolic link", path)
	}
	if _, err := os.Stat(targetPath); err != nil {
		return "", fmt.Errorf("removal path %s is unavailable: %w", path, err)
	}
	return targetPath, nil
}

func (m *Manager) findRegisteredRemoval(ctx context.Context, path, targetPath string) (WorktreeInfo, string, error) {
	entries, err := m.listUnlocked(ctx)
	if err != nil {
		return WorktreeInfo{}, "", fmt.Errorf("cannot validate removal registration: %w", err)
	}
	rootPath, err := canonicalPath(m.rootDir)
	if err != nil {
		return WorktreeInfo{}, "", fmt.Errorf("cannot inspect manager root: %w", err)
	}
	for i := 0; i < len(entries); i++ {
		entryPath, pathErr := canonicalPath(entries[i].Path)
		if pathErr == nil && entryPath == targetPath {
			return entries[i], rootPath, nil
		}
	}
	return WorktreeInfo{}, "", fmt.Errorf("removal path %s is not a registered worktree owned by %s", path, m.rootDir)
}

func validateRegisteredRemoval(path, targetPath, rootPath string, match WorktreeInfo) error {
	if targetPath == rootPath {
		return fmt.Errorf("refusing removal of primary worktree %s", path)
	}
	if match.Bare || match.Detached || match.Locked || match.Prunable || match.Branch == "" {
		return fmt.Errorf("refusing removal of unsafe registered worktree %s", path)
	}
	return nil
}

func removalStatusHasOnlyIgnored(status string) bool {
	hasRecord := false
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		hasRecord = true
		if len(line) < 2 || line[:2] != "!!" {
			return false
		}
	}
	return hasRecord
}

func (m *Manager) checkRemovalFilters(ctx context.Context, path string) error {
	result, err := m.runEffectiveProbe(ctx, path, "config", "--get-regexp", `^core\.(attributesfile|excludesfile|fsmonitor)$`)
	output := string(result.Stdout)
	if err != nil && !isConfigNoMatch(result, err) {
		return fmt.Errorf("cannot inspect worktree filters at %s: %w", path, err)
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("refusing removal of worktree %s: global Git attributes, excludes, or fsmonitor could change inspection", path)
	}
	result, err = m.runEffectiveProbe(ctx, path, "config", "--get-regexp", `^filter\..*\.(clean|process|smudge)$`)
	output = string(result.Stdout)
	if err != nil && !isConfigNoMatch(result, err) {
		return fmt.Errorf("cannot inspect worktree filters at %s: %w", path, err)
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("refusing removal of worktree %s: configured filters could execute", path)
	}
	return nil
}

func isConfigNoMatch(result util.CommandBytes, err error) bool {
	if err == nil {
		return false
	}
	if len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

func (m *Manager) runEffectiveProbe(ctx context.Context, path string, args ...string) (util.CommandBytes, error) {
	probeCtx, err := util.WithCommandEnvironment(ctx, sanitizedGitEnvironment())
	if err != nil {
		return util.CommandBytes{}, err
	}
	probeCtx, cancel := context.WithTimeout(probeCtx, 5*time.Second)
	defer cancel()
	argv := args
	if len(args) == 0 || args[0] != "config" {
		argv = append([]string{"-c", "core.fsmonitor=false"}, args...)
	}
	return util.RunGitBytes(probeCtx, path, removalProbeMaxBytes, argv...)
}

func (m *Manager) checkRemovalGitlinks(ctx context.Context, path string) error {
	result, err := util.RunGitProbe(ctx, path, removalProbeMaxBytes, "ls-files", "--stage", "-z")
	if err != nil {
		return fmt.Errorf("cannot inspect Git links at %s: %w", path, err)
	}
	output := string(result.Stdout)
	for _, entry := range strings.Split(output, "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return fmt.Errorf("refusing removal of worktree %s: populated or nested Git links are not safely inspectable", path)
		}
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

	return m.listUnlocked(ctx)
}

func (m *Manager) listUnlocked(ctx context.Context) ([]WorktreeInfo, error) {
	out, err := m.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed listing worktrees: %w", err)
	}

	return parseWorktreeList(string(out))
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Prune executes 'git worktree prune' to clean up stale worktree administrative files, then
// deletes managed wt/ branches that no worktree has checked out and whose commits HEAD
// already contains.
//
// The sweep is what makes a removal that failed half-way converge: a wt/<id> branch left
// behind by a worktree that is gone was otherwise kept forever. It deletes with 'git branch
// -d' and only branches merged into HEAD, because safe removal preserves the branch on
// purpose so unpublished commits stay reachable; a branch carrying commits HEAD lacks is
// kept.
func (m *Manager) Prune(ctx context.Context) error {
	if m == nil {
		return ErrNilManager
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.runGit(ctx, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed pruning worktrees: %w", err)
	}
	orphans, err := m.mergedOrphanBranchesUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed finding orphaned worktree branches: %w", err)
	}
	var errs []error
	for i := 0; i < len(orphans) && i < MaxPorcelainLines; i++ {
		if _, err := m.runGit(ctx, "branch", "-d", orphans[i]); err != nil {
			errs = append(errs, fmt.Errorf("failed deleting orphaned branch %s: %w", orphans[i], err))
		}
	}
	return errors.Join(errs...)
}

// managedRefPattern selects every managed branch: for-each-ref matches a literal pattern
// that ends in a slash as a prefix.
const managedRefPattern = "refs/heads/" + BranchPrefix

// mergedOrphanBranchesUnlocked returns the short names of managed branches that no
// registered worktree has checked out and that are merged into HEAD.
func (m *Manager) mergedOrphanBranchesUnlocked(ctx context.Context) ([]string, error) {
	// Listing first keeps a repository without managed branches off the --merged query,
	// which fails outright while HEAD is unborn.
	managed, err := m.refLines(ctx, "for-each-ref", "--format=%(refname)", managedRefPattern)
	if err != nil || len(managed) == 0 {
		return nil, err
	}
	merged, err := m.refLines(ctx, "for-each-ref", "--merged=HEAD", "--format=%(refname)", managedRefPattern)
	if err != nil {
		return nil, err
	}
	entries, err := m.listUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	checkedOut := make(map[string]bool, len(entries))
	for i := 0; i < len(entries); i++ {
		checkedOut[entries[i].Ref] = true
	}
	orphans := make([]string, 0, len(merged))
	for i := 0; i < len(merged); i++ {
		if !checkedOut[merged[i]] {
			orphans = append(orphans, strings.TrimPrefix(merged[i], "refs/heads/"))
		}
	}
	return orphans, nil
}

// refLines runs a git query printing one ref per line and returns the non-empty lines,
// bounded by MaxPorcelainLines (HISS-02).
func (m *Manager) refLines(ctx context.Context, args ...string) ([]string, error) {
	out, err := m.runGit(ctx, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > MaxPorcelainLines {
		return nil, fmt.Errorf("%w: line count %d > %d", ErrLimitExceeded, len(lines), MaxPorcelainLines)
	}
	refs := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if ref := strings.TrimSpace(lines[i]); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// runGit executes git through the audited util.RunGit entry point (HISS-02: the
// call always carries a deadline; DefaultGitTimeout applies when the caller's context
// has none). The arguments are fixed by this package and validated task ids or branch
// names, never free-form user input.
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
	mutatingCtx, err := util.WithCommandEnvironment(execCtx, sanitizedGitEnvironment())
	if err != nil {
		return nil, fmt.Errorf("prepare git environment: %w", err)
	}
	out, err := util.RunGit(mutatingCtx, m.rootDir, gitArgs...)
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w (output: %s)", strings.Join(args, " "), err, out)
	}
	return []byte(out), nil
}

// sanitizedGitEnvironment is stricter than util.RunCommand's default scrub: worktree
// mutations drop every inherited GIT_* variable except the global and system config
// selectors, not only the ones that bind git to a repository.
func sanitizedGitEnvironment() []string {
	return util.FilterEnvironment(os.Environ(), func(name string) bool {
		return strings.HasPrefix(name, "GIT_") && name != "GIT_CONFIG_GLOBAL" &&
			name != "GIT_CONFIG_SYSTEM" && name != "GIT_CONFIG_NOSYSTEM"
	})
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

// validateBaseBranch checks the value exactly as it is handed to git, not a trimmed copy.
//
// util.ValidateExecArg refuses a leading '-' (git would read "--force" as an option and
// create the worktree from HEAD), control bytes, backslashes and shell metacharacters. The
// remaining checks are the ref-name characters git itself rejects; a space among them also
// refuses a padded value such as " main". Control bytes are checked by value: a
// "\x00-\x1f" span inside a ContainsAny set is three characters, not a range, and its
// literal '-' rejected every hyphenated base branch such as release-1.0.
func validateBaseBranch(baseBranch string) error {
	if strings.TrimSpace(baseBranch) == "" {
		return ErrEmptyBaseBranch
	}
	if err := util.ValidateExecArg(baseBranch); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidBaseBranch, err)
	}
	if strings.Contains(baseBranch, "..") || strings.ContainsAny(baseBranch, " ~^:?*[") {
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
