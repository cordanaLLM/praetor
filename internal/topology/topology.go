package topology

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	// ErrScanTruncated indicates cleanup refused to act on an audit that did not cover the
	// whole tree; the report's TruncationReasons name the unaudited parts.
	ErrScanTruncated = errors.New("topology audit incomplete; cleanup refused")
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
// A match is an audit finding; whether cleanup may remove it is decided by autoCleanNames.
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

// autoCleanNames is the subset of StrayGovernanceNames whose name alone identifies an
// artifact Praetor manages: its configuration and state files, the lefthook config it
// installs, and the agent-instruction files compile-context emits. Every other name in
// StrayGovernanceNames (docs, lua, Makefile, .config, .github, .vscode, .editorconfig, ...)
// is shared with ordinary operator content, so a finding under it needs manual review
// (BUG-320).
var autoCleanNames = map[string]bool{
	"agents.md":                 true,
	"claude.md":                 true,
	"lefthook.yml":              true,
	".needs.yaml":               true,
	".standards-baseline.json":  true,
	".standards.lock":           true,
	"standards.sublime-project": true,
	".standards.yaml":           true,
	".windsurfrules":            true,
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
	// Truncated is true when a MaxScanEntries bound or an unreadable directory left part of
	// the tree unaudited. The findings are then a lower bound, not an exhaustive result.
	Truncated         bool     `json:"truncated"`
	TruncationReasons []string `json:"truncation_reasons"`
}

func (r *TopologyReport) markTruncated(reason string) {
	r.Truncated = true
	r.TruncationReasons = append(r.TruncationReasons, reason)
}

// boundScanEntries caps a directory listing at MaxScanEntries (HISS-02) and records the
// cut, so a bounded scan is never reported as an exhaustive one (BUG-904).
func boundScanEntries(entries []os.DirEntry, scope string, report *TopologyReport) []os.DirEntry {
	if len(entries) <= MaxScanEntries {
		return entries
	}
	report.markTruncated(fmt.Sprintf("%s: scan stopped after %d of %d entries (MaxScanEntries)",
		scope, MaxScanEntries, len(entries)))
	return entries[:MaxScanEntries]
}

// scanInterrupted reports a cancelled or expired context. Every scan and deletion loop
// iteration checks it, so the caller's deadline bounds the whole operation (BUG-609).
func scanInterrupted(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("topology %s interrupted: %w", phase, err)
	}
	return nil
}

// AuditWorkstationTopology audits devRoot against the workstation topology rules it
// implements: DEV-01 (repositories live in organization folders) and DEV-02 (no root
// compatibility symlinks). DEV-03 through DEV-05 are not evaluated here. A context that ends
// mid-scan returns an error and no partial report.
func AuditWorkstationTopology(ctx context.Context, devRoot string) (*TopologyReport, error) {
	if err := scanInterrupted(ctx, "audit"); err != nil {
		return nil, err
	}

	normRoot, err := validateDevRoot(devRoot)
	if err != nil {
		return nil, err
	}

	report := &TopologyReport{
		DevRoot:           normRoot,
		ValidRepos:        make([]string, 0),
		OrgContainers:     make([]string, 0),
		Symlinks:          make([]string, 0),
		StrayFiles:        make([]StrayFile, 0),
		Violations:        make([]string, 0),
		TruncationReasons: make([]string, 0),
	}

	entries, err := os.ReadDir(normRoot)
	if err != nil {
		return nil, fmt.Errorf("read dev root: %w", err)
	}

	for _, entry := range boundScanEntries(entries, "dev root", report) {
		if err := scanInterrupted(ctx, "audit"); err != nil {
			return nil, err
		}
		entryPath := filepath.Join(normRoot, entry.Name())
		if err := processDevRootEntry(ctx, entry, entryPath, report); err != nil {
			return nil, err
		}
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

func processDevRootEntry(ctx context.Context, entry os.DirEntry, entryPath string, report *TopologyReport) error {
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
		return nil
	}

	if !entry.IsDir() {
		checkDevRootFile(entryPath, lowerName, entry.Type(), report)
		return nil
	}

	if KnownOrgContainers[lowerName] {
		report.OrgContainers = append(report.OrgContainers, entry.Name())
		return auditOrgContainer(ctx, entryPath, entry.Name(), report)
	}

	// Any unrecognized directory in dev root with .git is violating DEV-01
	_, err := os.Stat(filepath.Join(entryPath, ".git"))
	switch {
	case err == nil:
		report.Violations = append(report.Violations,
			fmt.Sprintf("DEV-01: repository %s is located directly in dev root instead of an org folder", entry.Name()))
	case !errors.Is(err, os.ErrNotExist):
		report.markTruncated(fmt.Sprintf("%s: DEV-01 not evaluated, git metadata could not be inspected: %v",
			entry.Name(), err))
	}
	return nil
}

func isSymlink(path string) bool {
	lInfo, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return lInfo.Mode()&os.ModeSymlink != 0
}

func checkDevRootFile(path, lowerName string, mode fs.FileMode, report *TopologyReport) {
	// Dev root is allowed to have AGENTS.md, workstation workspace, and snapshot inventory
	if lowerName == "agents.md" || strings.HasSuffix(lowerName, ".code-workspace") || strings.HasPrefix(lowerName, ".snapshot-inventory") {
		return
	}
	if !StrayGovernanceNames[lowerName] {
		return
	}
	finding := StrayFile{
		Path:           path,
		RelPath:        filepath.Base(path),
		Reason:         "governance file misplaced in dev root instead of repository",
		IsSafeToDelete: true,
	}
	if reason, reviewed := manualReviewReason(lowerName, mode, "dev root"); reviewed {
		finding.Reason = reason
		finding.IsSafeToDelete = false
	}
	report.StrayFiles = append(report.StrayFiles, finding)
}

// manualReviewReason decides whether a governance-named entry needs manual review before
// removal. Cleanup acts on a name match only for an autoCleanNames artifact that is a
// regular file or a symlink; a directory is never removed on its name alone, because the
// removal would recurse into whatever the operator keeps there (BUG-320).
func manualReviewReason(lowerName string, mode fs.FileMode, location string) (string, bool) {
	if mode.IsDir() {
		return fmt.Sprintf("governance-named directory in %s; a name match never authorizes recursive deletion, review manually", location), true
	}
	if !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
		return fmt.Sprintf("governance-named special file in %s; only regular files and symlinks are cleaned automatically", location), true
	}
	if !autoCleanNames[lowerName] {
		return fmt.Sprintf("governance-named entry in %s shares its name with ordinary operator content, review manually", location), true
	}
	return "", false
}

func auditOrgContainer(ctx context.Context, orgPath, orgName string, report *TopologyReport) error {
	lowerOrg := strings.ToLower(orgName)
	if lowerOrg == "scratch" || lowerOrg == "worktrees" {
		return nil
	}

	entries, err := os.ReadDir(orgPath)
	if err != nil {
		report.markTruncated(fmt.Sprintf("organization container %s could not be read: %v", orgName, err))
		return nil
	}
	entries = boundScanEntries(entries, "organization container "+orgName, report)
	orgGitState, orgGitErr := inspectWorktreeGitMetadata(orgPath)

	hasChildRepos := false
	for _, entry := range entries {
		if err := scanInterrupted(ctx, "audit"); err != nil {
			return err
		}
		childPath := filepath.Join(orgPath, entry.Name())
		if entry.IsDir() && HasValidGitRepo(childPath) {
			hasChildRepos = true
			report.ValidRepos = append(report.ValidRepos, filepath.Join(orgName, entry.Name()))
		}
	}

	if isProtectedGitState(orgGitState, orgGitErr) {
		auditStrayGitDir(filepath.Join(orgPath, ".git"), filepath.Join(orgName, ".git"),
			hasChildRepos, orgGitState, orgGitErr, report)
		return nil
	}
	return auditOrgStrayEntries(ctx, orgPath, orgName, entries, hasChildRepos, report)
}

func auditOrgStrayEntries(ctx context.Context, orgPath, orgName string, entries []os.DirEntry, hasChildRepos bool, report *TopologyReport) error {
	for _, entry := range entries {
		if err := scanInterrupted(ctx, "audit"); err != nil {
			return err
		}
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
	return nil
}

func auditGovernanceEntry(entry os.DirEntry, entryPath, relPath, orgName string, report *TopologyReport) {
	finding := StrayFile{
		Path:           entryPath,
		RelPath:        relPath,
		Reason:         fmt.Sprintf("stray governance file in organization container %s (DEV-01)", orgName),
		IsSafeToDelete: true,
	}
	if entry.IsDir() {
		state, inspectErr := inspectWorktreeGitMetadata(entryPath)
		if state == gitMetadataLive {
			return
		}
		if inspectErr != nil || state == gitMetadataUnknown {
			finding.Reason = "governance-named directory contains git metadata that could not be inspected safely"
			finding.IsSafeToDelete = false
			report.StrayFiles = append(report.StrayFiles, finding)
			return
		}
	}
	location := "organization container " + orgName + " (DEV-01)"
	if reason, reviewed := manualReviewReason(strings.ToLower(entry.Name()), entry.Type(), location); reviewed {
		finding.Reason = reason
		finding.IsSafeToDelete = false
	}
	report.StrayFiles = append(report.StrayFiles, finding)
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
	target, ok, err := resolveGitlinkTarget(worktree)
	if err != nil || !ok {
		return gitMetadataUnknown, err
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

// resolveGitlinkTarget reads the "gitdir: <path>" line of the .git file in worktree and
// returns the directory it names, cleaned and joined onto worktree when relative. ok is
// false when the file is no gitlink or its target is not an existing directory; err
// carries read and stat failures.
func resolveGitlinkTarget(worktree string) (target string, ok bool, err error) {
	data, err := util.ReadConfinedLimited(worktree, ".git", maxGitlinkBytes)
	if err != nil {
		return "", false, fmt.Errorf("read gitlink: %w", err)
	}
	line := strings.TrimRight(string(data), "\r\n")
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false, nil
	}
	target = line[len(prefix):]
	if target == "" {
		return "", false, nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stat gitlink target: %w", err)
	}
	return target, info.IsDir(), nil
}

// ErrNotCheckout is returned by GitCommonDir for a path HasValidGitRepo does not accept.
var ErrNotCheckout = errors.New("topology: not a git checkout")

// GitCommonDir returns the canonical git common directory of the checkout at path: the
// repository every linked worktree of one clone shares. A main checkout's common
// directory is its own .git; a linked worktree's gitlink names .git/worktrees/<name>,
// whose commondir file points back at that .git; a submodule's gitlink names
// .git/modules/<name>, which has no commondir file and is therefore a repository of its
// own. Two checkouts are worktrees of one repository exactly when their common
// directories are equal. The result is resolved through util.ResolveExistingPath, so
// aliased spellings of one directory compare equal. No git process is started.
func GitCommonDir(ctx context.Context, path string) (string, error) {
	gitDir, err := checkoutGitDir(path)
	if err != nil {
		return "", err
	}
	return resolveCommonDir(ctx, gitDir)
}

// checkoutGitDir returns the git directory of the checkout at path: its .git directory,
// or the directory its .git gitlink file names. The result is not canonicalised.
func checkoutGitDir(path string) (string, error) {
	if !HasValidGitRepo(path) {
		return "", fmt.Errorf("%w: %s", ErrNotCheckout, path)
	}
	gitDir := filepath.Join(path, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil {
		return "", fmt.Errorf("lstat git metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return gitDir, nil
	}
	target, ok, err := resolveGitlinkTarget(path)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: %s: gitlink target vanished", ErrNotCheckout, path)
	}
	return target, nil
}

// resolveCommonDir follows gitDir's commondir file when it has one and canonicalises the
// result.
func resolveCommonDir(ctx context.Context, gitDir string) (string, error) {
	common := gitDir
	data, err := util.ReadConfinedLimited(gitDir, "commondir", maxGitlinkBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", fmt.Errorf("read commondir of %s: %w", gitDir, err)
	default:
		if rel := strings.TrimSpace(string(data)); rel != "" {
			common = rel
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
		}
	}
	resolved, err := util.ResolveExistingPath(ctx, filepath.Clean(common))
	if err != nil {
		return "", fmt.Errorf("resolve git common directory %s: %w", common, err)
	}
	if !util.DirExists(resolved) {
		return "", fmt.Errorf("git common directory %q is not an existing directory", resolved)
	}
	return resolved, nil
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
// require manual review without changing the legacy CleanWorkstationTopology contract. It
// refuses to act on a truncated audit (ErrScanTruncated) and stops between removals once
// ctx ends, returning the removals made so far with the context error.
func CleanWorkstationTopologyDetailed(ctx context.Context, devRoot string, dryRun bool) (*CleanResult, error) {
	report, err := AuditWorkstationTopology(ctx, devRoot)
	if err != nil {
		return nil, fmt.Errorf("audit failed before clean: %w", err)
	}
	if report.Truncated {
		return nil, fmt.Errorf("%w: %s", ErrScanTruncated, strings.Join(report.TruncationReasons, "; "))
	}

	result := &CleanResult{
		Cleaned: make([]string, 0),
		Blocked: make([]StrayFile, 0),
	}
	for _, stray := range report.StrayFiles {
		if err := scanInterrupted(ctx, "clean"); err != nil {
			return result, err
		}
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
	// The audit marks no directory safe except proven-headless .git metadata. A directory
	// under any other name here means the tree changed after the audit: refuse (BUG-320).
	if !strings.EqualFold(filepath.Base(cleanPath), ".git") {
		return fmt.Errorf("cannot recursively delete a directory that is not git metadata: %s", cleanPath)
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
