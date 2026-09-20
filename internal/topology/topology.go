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

// TopologyReport contains the full audit results for the workstation dev tree.
type TopologyReport struct {
	DevRoot       string      `json:"dev_root"`
	ValidRepos    []string    `json:"valid_repos"`
	OrgContainers []string    `json:"org_containers"`
	Symlinks      []string    `json:"symlinks"`
	StrayFiles    []StrayFile `json:"stray_files"`
	Violations    []string    `json:"violations"`
}

// AuditWorkstationTopology audits devRoot against workstation contract DEV-01 through DEV-05.
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
			auditStrayGitDir(entryPath, relPath, hasChildRepos, report)
			continue
		}

		if StrayGovernanceNames[lowerName] {
			// Verify it is not a genuine child repository
			if entry.IsDir() && HasValidGitRepo(entryPath) {
				continue
			}
			report.StrayFiles = append(report.StrayFiles, StrayFile{
				Path:           entryPath,
				RelPath:        relPath,
				Reason:         fmt.Sprintf("stray governance file in organization container %s (DEV-01)", orgName),
				IsSafeToDelete: true,
			})
		}
	}
}

func auditStrayGitDir(entryPath, relPath string, hasChildRepos bool, report *TopologyReport) {
	headPath := filepath.Join(entryPath, "HEAD")
	_, headErr := os.Stat(headPath)
	isHeadless := os.IsNotExist(headErr)

	if isHeadless || hasChildRepos {
		report.StrayFiles = append(report.StrayFiles, StrayFile{
			Path:           entryPath,
			RelPath:        relPath,
			Reason:         "stray or headless .git directory in organization container",
			IsSafeToDelete: true,
		})
	}
}

// HasValidGitRepo reports whether path is a git working tree: a .git gitlink file whose
// gitdir target is a directory with a regular HEAD, or a .git directory with the same
// property. An empty path is never a repository, including when the process working
// directory itself is a checkout.
func HasValidGitRepo(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.Mode().IsRegular() {
		return hasValidGitlinkTarget(path)
	}
	if !info.IsDir() {
		return false
	}
	return hasRegularHead(gitPath)
}

// hasValidGitlinkTarget reports whether a .git file resolves to a git directory with a
// regular HEAD. Relative targets resolve against the worktree, matching Git's layout.
func hasValidGitlinkTarget(worktree string) bool {
	data, err := util.ReadConfinedLimited(worktree, ".git", maxGitlinkBytes)
	if err != nil {
		return false
	}
	line := strings.TrimRight(string(data), "\r\n")
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	target := line[len(prefix):]
	if target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	return err == nil && info.IsDir() && hasRegularHead(target)
}

func hasRegularHead(gitDir string) bool {
	info, err := os.Stat(filepath.Join(gitDir, "HEAD"))
	return err == nil && info.Mode().IsRegular()
}

// CleanWorkstationTopology removes identified stray files and directories adhering to strict safety rules.
func CleanWorkstationTopology(ctx context.Context, devRoot string, dryRun bool) ([]string, error) {
	report, err := AuditWorkstationTopology(ctx, devRoot)
	if err != nil {
		return nil, fmt.Errorf("audit failed before clean: %w", err)
	}

	cleaned := make([]string, 0)
	for _, stray := range report.StrayFiles {
		if !stray.IsSafeToDelete {
			continue
		}

		if err := verifyDeletionSafety(report.DevRoot, stray.Path); err != nil {
			return nil, fmt.Errorf("safety check rejected deletion of %s: %w", stray.Path, err)
		}

		if !dryRun {
			if err := removeStrayEntry(stray.Path); err != nil {
				return cleaned, err
			}
		}
		cleaned = append(cleaned, stray.Path)
	}

	return cleaned, nil
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

	// Never delete dev root itself
	if cleanPath == cleanDev {
		return errors.New("cannot delete dev root")
	}

	// Never delete an organization container itself
	parentDir := filepath.Dir(cleanPath)
	if parentDir == cleanDev && !isSymlink(cleanPath) {
		baseName := strings.ToLower(filepath.Base(cleanPath))
		if KnownOrgContainers[baseName] {
			return fmt.Errorf("cannot delete recognized organization container: %s", cleanPath)
		}
	}

	// Never delete a directory containing a valid leaf git repository (unless it is a stray symlink)
	if !isSymlink(cleanPath) && HasValidGitRepo(cleanPath) {
		return fmt.Errorf("cannot delete directory with valid git repository: %s", cleanPath)
	}

	return nil
}
