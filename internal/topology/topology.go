package topology

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxScanEntries limits directory scan iterations to prevent unbounded execution (HISS-02).
	MaxScanEntries = 1000
	// maxGitlinkBytes caps a .git gitlink file. Real gitlinks are one short line.
	maxGitlinkBytes = 4096
)

var (
	// ErrDevRootNotExist indicates the dev directory does not exist.
	ErrDevRootNotExist = errors.New("dev root directory does not exist")
	// ErrDevRootNotDir indicates the dev root path is not a directory.
	ErrDevRootNotDir = errors.New("dev root path is not a directory")
)

// KnownOrgContainers lists recognized organization directories under dev root (DEV-01).
var KnownOrgContainers = map[string]bool{
	"cordanallm": true,
	"lusoris":    true,
	"vmafx":      true,
	"golusoris":  true,
	"upstream":   true,
	"local":      true,
	"stacks":     true,
	"worktrees":  true,
	"scratch":    true,
}

// StrayGovernanceNames lists governance artifacts that must not exist in org containers or dev root.
var StrayGovernanceNames = map[string]bool{
	".agents":                   true,
	"agents.md":                 true,
	".clang-tidy":               true,
	"claude.md":                 true,
	".codex":                    true,
	".config":                   true,
	"contributing.md":           true,
	".cursor":                   true,
	".devcontainer":             true,
	".dir-locals.el":            true,
	"docs":                      true,
	".editorconfig":             true,
	".fleet":                    true,
	".gemini":                   true,
	".github":                   true,
	".gitignore":                true,
	".helix":                    true,
	".idea":                     true,
	"lefthook.yml":              true,
	"lua":                       true,
	"makefile":                  true,
	".needs.yaml":               true,
	".nvim.lua":                 true,
	".paperclip":                true,
	"pre_migration_epic.md":     true,
	"security.md":               true,
	".standards-baseline.json":  true,
	".standards.lock":           true,
	"standards.sublime-project": true,
	".standards.yaml":           true,
	".vscode":                   true,
	".windsurfrules":            true,
	".zed":                      true,
}

// StrayFile represents an misplaced file or directory violating workstation topology.
type StrayFile struct {
	Path           string `json:"path"`
	RelPath        string `json:"rel_path"`
	Reason         string `json:"reason"`
	IsSafeToDelete bool   `json:"is_safe_to_delete"`
}

// CleanResult records both entries accepted by the deletion boundary and findings that
// require manual review. A clean operation is complete only when Blocked is empty.
type CleanResult struct {
	Cleaned []string
	Blocked []StrayFile
}

type deletionSafetyError struct {
	err error
}

func (e *deletionSafetyError) Error() string {
	return e.err.Error()
}

func (e *deletionSafetyError) Unwrap() error {
	return e.err
}

type gitMetadataState uint8

const (
	gitMetadataUnknown gitMetadataState = iota
	gitMetadataAbsent
	gitMetadataHeadless
	gitMetadataLive
)

// TopologyReport contains the full audit results for the workstation dev tree.
type TopologyReport struct {
	DevRoot       string      `json:"dev_root"`
	ValidRepos    []string    `json:"valid_repos"`
	OrgContainers []string    `json:"org_containers"`
	Symlinks      []string    `json:"symlinks"`
	StrayFiles    []StrayFile `json:"stray_files"`
	Violations    []string    `json:"violations"`
}

// AuditWorkstationTopology audits devRoot against the workstation topology rules it
// implements: DEV-01 (repositories live in organization folders) and DEV-02 (no root
// compatibility symlinks). DEV-03 through DEV-05 are not evaluated here.
func AuditWorkstationTopology(ctx context.Context, devRoot string) (*TopologyReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled: %w", err)
	}

	normRoot, err := validateDevRoot(devRoot)
	if err != nil {
		return nil, err
	}

	report := &TopologyReport{
		DevRoot:       normRoot,
		ValidRepos:    make([]string, 0),
		OrgContainers: make([]string, 0),
		Symlinks:      make([]string, 0),
		StrayFiles:    make([]StrayFile, 0),
		Violations:    make([]string, 0),
	}

	entries, err := os.ReadDir(normRoot)
	if err != nil {
		return nil, fmt.Errorf("read dev root: %w", err)
	}

	scanCount := 0
	for _, entry := range entries {
		if scanCount >= MaxScanEntries {
			break
		}
		scanCount++

		entryPath := filepath.Join(normRoot, entry.Name())
		processDevRootEntry(ctx, normRoot, entry, entryPath, report)
	}

	return report, nil
}

func validateDevRoot(devRoot string) (string, error) {
	normRoot, err := filepath.Abs(devRoot)
	if err != nil {
		return "", fmt.Errorf("resolve dev root: %w", err)
	}

	info, err := os.Stat(normRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrDevRootNotExist
		}
		return "", fmt.Errorf("stat dev root: %w", err)
	}
	if !info.IsDir() {
		return "", ErrDevRootNotDir
	}
	return normRoot, nil
}

func processDevRootEntry(ctx context.Context, normRoot string, entry os.DirEntry, entryPath string, report *TopologyReport) {
	lowerName := strings.ToLower(entry.Name())

	// Check if entry is a symlink (DEV-02: root compatibility symlinks are prohibited)
	if isSymlink(entryPath) {
		report.Symlinks = append(report.Symlinks, entry.Name())
		report.StrayFiles = append(report.StrayFiles, StrayFile{
			Path:           entryPath,
			RelPath:        entry.Name(),
			Reason:         fmt.Sprintf("deprecated root compatibility symlink %s (DEV-02)", entry.Name()),
			IsSafeToDelete: true,
		})
		report.Violations = append(report.Violations,
			fmt.Sprintf("DEV-02: root compatibility symlink %s violates canonical path invariant", entry.Name()))
		return
	}

	if !entry.IsDir() {
		checkDevRootFile(entryPath, lowerName, report)
		return
	}

	if KnownOrgContainers[lowerName] {
		report.OrgContainers = append(report.OrgContainers, entry.Name())
		auditOrgContainer(ctx, normRoot, entryPath, entry.Name(), report)
		return
	}

	// Any unrecognized directory in dev root with .git is violating DEV-01
	childGit := filepath.Join(entryPath, ".git")
	if _, err := os.Stat(childGit); err == nil {
		report.Violations = append(report.Violations,
			fmt.Sprintf("DEV-01: repository %s is located directly in dev root instead of an org folder", entry.Name()))
	}
}

func isSymlink(path string) bool {
	lInfo, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return lInfo.Mode()&os.ModeSymlink != 0
}

func checkDevRootFile(path, lowerName string, report *TopologyReport) {
	// Dev root is allowed to have AGENTS.md, workstation workspace, and snapshot inventory
	if lowerName == "agents.md" || strings.HasSuffix(lowerName, ".code-workspace") || strings.HasPrefix(lowerName, ".snapshot-inventory") {
		return
	}
	if StrayGovernanceNames[lowerName] {
		report.StrayFiles = append(report.StrayFiles, StrayFile{
			Path:           path,
			RelPath:        filepath.Base(path),
			Reason:         "governance file misplaced in dev root instead of repository",
			IsSafeToDelete: true,
		})
	}
}

func auditOrgContainer(ctx context.Context, devRoot, orgPath, orgName string, report *TopologyReport) {
	lowerOrg := strings.ToLower(orgName)
	if lowerOrg == "scratch" || lowerOrg == "worktrees" {
		return
	}

	entries, err := os.ReadDir(orgPath)
	if err != nil {
		return
	}
	orgGitState, orgGitErr := inspectWorktreeGitMetadata(orgPath)

	scanCount := 0
	hasChildRepos := false
	for _, entry := range entries {
		if scanCount >= MaxScanEntries {
			break
		}
		scanCount++

		childPath := filepath.Join(orgPath, entry.Name())
		if entry.IsDir() && HasValidGitRepo(childPath) {
			hasChildRepos = true
			report.ValidRepos = append(report.ValidRepos, filepath.Join(orgName, entry.Name()))
		}
	}

	if isProtectedGitState(orgGitState, orgGitErr) {
		auditStrayGitDir(filepath.Join(orgPath, ".git"), filepath.Join(orgName, ".git"),
			hasChildRepos, orgGitState, orgGitErr, report)
		return
	}
	auditOrgStrayEntries(orgPath, orgName, entries, hasChildRepos, report)
}

func auditOrgStrayEntries(orgPath, orgName string, entries []os.DirEntry, hasChildRepos bool, report *TopologyReport) {
	scanCount := 0
	for _, entry := range entries {
		if scanCount >= MaxScanEntries {
			break
		}
		scanCount++

		name := entry.Name()
		lowerName := strings.ToLower(name)
		entryPath := filepath.Join(orgPath, name)
		relPath := filepath.Join(orgName, name)

		if lowerName == ".git" {
			state, inspectErr := inspectGitMetadata(entryPath)
			auditStrayGitDir(entryPath, relPath, hasChildRepos, state, inspectErr, report)
			continue
		}

		if StrayGovernanceNames[lowerName] {
			auditGovernanceEntry(entry, entryPath, relPath, orgName, report)
		}
	}
}

func auditGovernanceEntry(entry os.DirEntry, entryPath, relPath, orgName string, report *TopologyReport) {
	reason := fmt.Sprintf("stray governance file in organization container %s (DEV-01)", orgName)
	safe := true
	if entry.IsDir() {
		state, inspectErr := inspectWorktreeGitMetadata(entryPath)
		if state == gitMetadataLive {
			return
		}
		if inspectErr != nil || state == gitMetadataUnknown {
			reason = "governance-named directory contains git metadata that could not be inspected safely"
			safe = false
		}
	}
	report.StrayFiles = append(report.StrayFiles, StrayFile{
		Path:           entryPath,
		RelPath:        relPath,
		Reason:         reason,
		IsSafeToDelete: safe,
	})
}

func auditStrayGitDir(
	entryPath, relPath string,
	hasChildRepos bool,
	state gitMetadataState,
	inspectErr error,
	report *TopologyReport,
) {
	if state == gitMetadataHeadless {
		report.StrayFiles = append(report.StrayFiles, StrayFile{
			Path:           entryPath,
			RelPath:        relPath,
			Reason:         "headless .git directory in organization container",
			IsSafeToDelete: true,
		})
		return
	}
	if state == gitMetadataLive && !hasChildRepos {
		return
	}
	reason := "git metadata requires manual review"
	if state == gitMetadataLive {
		reason = "organization container is also a live git repository"
	} else if inspectErr != nil {
		reason = "git metadata could not be inspected safely"
	}
	report.StrayFiles = append(report.StrayFiles, StrayFile{
		Path:           entryPath,
		RelPath:        relPath,
		Reason:         reason,
		IsSafeToDelete: false,
	})
}

func isProtectedGitState(state gitMetadataState, inspectErr error) bool {
	return inspectErr != nil || state == gitMetadataUnknown || state == gitMetadataLive
}

func inspectGitMetadata(gitPath string) (gitMetadataState, error) {
	info, err := os.Lstat(gitPath)
	if errors.Is(err, os.ErrNotExist) {
		return gitMetadataAbsent, nil
	}
	if err != nil {
		return gitMetadataUnknown, fmt.Errorf("lstat git metadata: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return gitMetadataUnknown, nil
	}
	if info.Mode().IsRegular() {
		return inspectGitlink(filepath.Dir(gitPath))
	}
	if !info.IsDir() {
		return gitMetadataUnknown, nil
	}
	return inspectGitDirectory(gitPath)
}

func inspectGitDirectory(gitPath string) (gitMetadataState, error) {
	headInfo, err := os.Lstat(filepath.Join(gitPath, "HEAD"))
	if errors.Is(err, os.ErrNotExist) {
		return gitMetadataHeadless, nil
	}
	if err != nil {
		return gitMetadataUnknown, fmt.Errorf("lstat git HEAD: %w", err)
	}
	if headInfo.Mode().IsRegular() || headInfo.Mode()&os.ModeSymlink != 0 {
		return gitMetadataLive, nil
	}
	return gitMetadataUnknown, nil
}

func inspectWorktreeGitMetadata(path string) (gitMetadataState, error) {
	if strings.TrimSpace(path) == "" {
		return gitMetadataAbsent, nil
	}
	return inspectGitMetadata(filepath.Join(path, ".git"))
}

func inspectGitlink(worktree string) (gitMetadataState, error) {
	data, err := util.ReadConfinedLimited(worktree, ".git", maxGitlinkBytes)
	if err != nil {
		return gitMetadataUnknown, fmt.Errorf("read gitlink: %w", err)
	}
	line := strings.TrimRight(string(data), "\r\n")
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return gitMetadataUnknown, nil
	}
	target := line[len(prefix):]
	if target == "" {
		return gitMetadataUnknown, nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return gitMetadataUnknown, nil
	}
	if err != nil {
		return gitMetadataUnknown, fmt.Errorf("stat gitlink target: %w", err)
	}
	if !info.IsDir() {
		return gitMetadataUnknown, nil
	}
	state, inspectErr := inspectGitDirectory(target)
	if inspectErr != nil {
		return gitMetadataUnknown, inspectErr
	}
	if state != gitMetadataLive {
		return gitMetadataUnknown, nil
	}
	return gitMetadataLive, nil
}

// HasValidGitRepo reports whether path is a git working tree whose directory or gitlink
// metadata has a regular or legacy symbolic HEAD. An empty path is never a repository,
// including when the process working directory itself is a checkout.
func HasValidGitRepo(path string) bool {
	state, err := inspectWorktreeGitMetadata(path)
	return err == nil && state == gitMetadataLive
}

// CleanWorkstationTopology removes safe stray files and returns their paths. Unsafe
// findings remain untouched; callers that need them use CleanWorkstationTopologyDetailed.
func CleanWorkstationTopology(ctx context.Context, devRoot string, dryRun bool) ([]string, error) {
	result, err := CleanWorkstationTopologyDetailed(ctx, devRoot, dryRun)
	if result == nil {
		return nil, err
	}
	var safetyErr *deletionSafetyError
	if errors.As(err, &safetyErr) {
		return nil, err
	}
	return result.Cleaned, err
}

// CleanWorkstationTopologyDetailed removes safe stray files and reports findings that
// require manual review without changing the legacy CleanWorkstationTopology contract.
func CleanWorkstationTopologyDetailed(ctx context.Context, devRoot string, dryRun bool) (*CleanResult, error) {
	report, err := AuditWorkstationTopology(ctx, devRoot)
	if err != nil {
		return nil, fmt.Errorf("audit failed before clean: %w", err)
	}

	result := &CleanResult{
		Cleaned: make([]string, 0),
		Blocked: make([]StrayFile, 0),
	}
	for _, stray := range report.StrayFiles {
		if !stray.IsSafeToDelete {
			result.Blocked = append(result.Blocked, stray)
			continue
		}

		if err := verifyDeletionSafety(report.DevRoot, stray.Path); err != nil {
			return result, &deletionSafetyError{err: fmt.Errorf(
				"safety check rejected deletion of %s: %w", stray.Path, err)}
		}

		if !dryRun {
			if err := removeStrayEntry(stray.Path); err != nil {
				return result, err
			}
		}
		result.Cleaned = append(result.Cleaned, stray.Path)
	}

	return result, nil
}

func removeStrayEntry(path string) error {
	if isSymlink(path) {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove stray symlink %s: %w", path, err)
		}
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove stray entry %s: %w", path, err)
	}
	return nil
}

func verifyDeletionSafety(devRoot, path string) error {
	cleanDev := filepath.Clean(devRoot)
	cleanPath := filepath.Clean(path)
	if err := verifyDeletionLocation(cleanDev, cleanPath); err != nil {
		return err
	}
	pathInfo, err := os.Lstat(cleanPath)
	if err != nil {
		return fmt.Errorf("cannot inspect deletion candidate: %w", err)
	}
	pathIsSymlink := pathInfo.Mode()&os.ModeSymlink != 0
	if err := verifyOrganizationContainerTarget(cleanDev, cleanPath, pathIsSymlink); err != nil {
		return err
	}
	if err := verifyOrganizationGitBoundary(cleanDev, cleanPath); err != nil {
		return err
	}
	if err := verifyDirectGitMetadataTarget(cleanPath); err != nil {
		return err
	}
	if pathIsSymlink || !pathInfo.IsDir() {
		return nil
	}
	return verifyWorktreeDeletionTarget(cleanPath)
}

func verifyDeletionLocation(devRoot, candidate string) error {
	if candidate == devRoot {
		return errors.New("cannot delete dev root")
	}
	relPath, err := filepath.Rel(devRoot, candidate)
	if err != nil {
		return fmt.Errorf("resolve deletion candidate relative to dev root: %w", err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("cannot delete path outside dev root: %s", candidate)
	}
	return nil
}

func verifyOrganizationContainerTarget(devRoot, candidate string, isSymlink bool) error {
	if filepath.Dir(candidate) != devRoot || isSymlink {
		return nil
	}
	if KnownOrgContainers[strings.ToLower(filepath.Base(candidate))] {
		return fmt.Errorf("cannot delete recognized organization container: %s", candidate)
	}
	return nil
}

func verifyOrganizationGitBoundary(devRoot, candidate string) error {
	orgPath, found, err := organizationForCandidate(devRoot, candidate)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	state, inspectErr := inspectWorktreeGitMetadata(orgPath)
	if inspectErr != nil {
		return fmt.Errorf("cannot inspect organization git metadata safely: %w", inspectErr)
	}
	if state == gitMetadataLive || state == gitMetadataUnknown {
		return fmt.Errorf("cannot delete content beneath live or indeterminate organization repository: %s", candidate)
	}
	return nil
}

func organizationForCandidate(devRoot, candidate string) (string, bool, error) {
	relPath, err := filepath.Rel(devRoot, candidate)
	if err != nil {
		return "", false, fmt.Errorf("resolve organization candidate: %w", err)
	}
	parts := strings.Split(relPath, string(os.PathSeparator))
	if len(parts) < 2 || !KnownOrgContainers[strings.ToLower(parts[0])] {
		return "", false, nil
	}
	return filepath.Join(devRoot, parts[0]), true, nil
}

func verifyDirectGitMetadataTarget(candidate string) error {
	if !strings.EqualFold(filepath.Base(candidate), ".git") {
		return nil
	}
	state, inspectErr := inspectGitMetadata(candidate)
	if inspectErr != nil {
		return fmt.Errorf("cannot inspect git metadata safely: %w", inspectErr)
	}
	if state != gitMetadataHeadless {
		return fmt.Errorf("cannot delete live or indeterminate git metadata: %s", candidate)
	}
	return nil
}

func verifyWorktreeDeletionTarget(candidate string) error {
	state, inspectErr := inspectWorktreeGitMetadata(candidate)
	if inspectErr != nil {
		return fmt.Errorf("cannot inspect nested git metadata safely: %w", inspectErr)
	}
	if state == gitMetadataLive || state == gitMetadataUnknown {
		return fmt.Errorf("cannot delete directory with live or indeterminate git metadata: %s", candidate)
	}
	return nil
}
