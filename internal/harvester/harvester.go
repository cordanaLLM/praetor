package harvester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxDevScanEntries bounds the dev directory iteration (HISS-02).
	MaxDevScanEntries = 500
	// MaxWorktreeScan bounds the per-container worktree iteration (HISS-02).
	MaxWorktreeScan = 200
	// DefaultStaleWorktreeAge is the idle time after which an ephemeral worktree counts as
	// stale. This observation does not establish owner release or GC eligibility.
	DefaultStaleWorktreeAge = 24 * time.Hour
	inventoryGitOutputLimit = 64 * 1024
	// MaxRepositoryObservations bounds the complete report across the whole scan.
	MaxRepositoryObservations = 1000
	// MaxRepositoryRemotes bounds remote identities retained for one repository.
	MaxRepositoryRemotes = 32
)

// RepoMetadata captures governance and technical discovery attributes for a repository.
type RepoMetadata struct {
	Owner           string   `json:"owner"`
	Name            string   `json:"name"`
	IsPrivate       bool     `json:"is_private"`
	PrimaryLanguage string   `json:"primary_language"`
	Description     string   `json:"description"`
	Archetype       string   `json:"archetype"`
	RulesCoverage   []string `json:"rules_coverage"`
	WorktreesCount  int      `json:"worktrees_count"`
}

// FleetReport consolidates multi-org repository discovery results.
type FleetReport struct {
	TotalRepos            int            `json:"total_repos"`
	ReposByOrg            map[string]int `json:"repos_by_org"`
	ArchetypeDistribution map[string]int `json:"archetype_distribution"`
	Repos                 []RepoMetadata `json:"repos"`
	Timestamp             time.Time      `json:"timestamp"`
}

// WorkstationReport captures local machine dev directory and worktree states.
type WorkstationReport struct {
	DevReposCount int `json:"dev_repos_count"`
	// StaleWorktrees lists worktrees idle for longer than DefaultStaleWorktreeAge.
	StaleWorktrees               []string                `json:"stale_worktrees"`
	MissingRulesRepos            []string                `json:"missing_rules_repos"`
	DirtyRepos                   []string                `json:"dirty_repos"`
	DiscoveredAgentDoc           []string                `json:"discovered_agent_doc"`
	RepositoryObservations       []RepositoryObservation `json:"repository_observations"`
	RepositoryInventoryComplete  bool                    `json:"repository_inventory_complete"`
	RepositoryInventoryTruncated bool                    `json:"repository_inventory_truncated"`
	RepositoryInventoryErrors    []string                `json:"repository_inventory_errors,omitempty"`
	RepositoryInventoryScope     string                  `json:"repository_inventory_scope"`
}

// RepositoryObservation is a read-only, local identity record. Probe failures remain
// explicit through the state fields and never become a local-only classification.
type RepositoryObservation struct {
	Path                     string   `json:"path"`
	GitCommonDir             string   `json:"git_common_dir,omitempty"`
	Classification           string   `json:"classification"`
	RemoteURLs               []string `json:"remote_urls,omitempty"`
	RemoteState              string   `json:"remote_state"`
	DirtyEntries             int      `json:"dirty_entries,omitempty"`
	DirtyState               string   `json:"dirty_state"`
	DirtyScope               string   `json:"dirty_scope"`
	ProbeErrors              []string `json:"probe_errors,omitempty"`
	WorkingdirPresent        bool     `json:"workingdir_present"`
	WorkingdirProbeIgnored   *bool    `json:"workingdir_probe_ignored,omitempty"`
	WorkingdirTrackedEntries int      `json:"workingdir_tracked_entries,omitempty"`
	WorkingdirTrackedState   string   `json:"workingdir_tracked_state"`
}

// archetypeHint maps description keywords and language names to an archetype. Rules are
// evaluated in order; the first match wins.
type archetypeHint struct {
	keywords  []string // matched as substrings of the lower-cased description
	languages []string // matched exactly against the lower-cased language
	archetype string
}

// maxArchetypeHints bounds the hint table scan (HISS-02).
const maxArchetypeHints = 16

// archetypeHints returns the detection table in priority order.
func archetypeHints() []archetypeHint {
	return []archetypeHint{
		{keywords: []string{"gpu", "vulkan", "ffmpeg", "kernel", "sycl", "cuda"}, archetype: "native-gpu-systems"},
		{keywords: []string{"kubernetes", "argocd", "terraform", "gitops", "helm"}, archetype: "gitops-infra"},
		{keywords: []string{"framework", "composable"}, archetype: "framework"},
		{keywords: []string{"client", "sdk", "modules"}, archetype: "library-client"},
		{keywords: []string{"static", "pages"}, languages: []string{"astro"}, archetype: "pages-site"},
		{languages: []string{"rust", "go", "python", "typescript", "dart", "flutter", "java", "kotlin"}, archetype: "app-service"},
	}
}

// matches reports whether the hint applies to the lower-cased language and description.
func (h archetypeHint) matches(lowerLang, lowerDesc string) bool {
	for i := 0; i < len(h.keywords) && i < maxArchetypeHints; i++ {
		if strings.Contains(lowerDesc, h.keywords[i]) {
			return true
		}
	}
	for i := 0; i < len(h.languages) && i < maxArchetypeHints; i++ {
		if lowerLang == h.languages[i] {
			return true
		}
	}
	return false
}

// DetectArchetype recommends an archetype based on language and descriptions.
func DetectArchetype(lang, desc string) string {
	lowerDesc := strings.ToLower(desc)
	lowerLang := strings.ToLower(lang)
	hints := archetypeHints()
	for i := 0; i < len(hints) && i < maxArchetypeHints; i++ {
		if hints[i].matches(lowerLang, lowerDesc) {
			return hints[i].archetype
		}
	}
	return "template-seed"
}

// ScanLocalWorkstation audits directories under devDir for repos and worktrees.
func ScanLocalWorkstation(ctx context.Context, devDir string) (*WorkstationReport, error) {
	if ctx == nil {
		return nil, errors.New("workstation scan requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before scan: %w", err)
	}

	report := &WorkstationReport{
		StaleWorktrees:              make([]string, 0),
		MissingRulesRepos:           make([]string, 0),
		DirtyRepos:                  make([]string, 0),
		DiscoveredAgentDoc:          make([]string, 0),
		RepositoryObservations:      make([]RepositoryObservation, 0),
		RepositoryInventoryComplete: true,
		RepositoryInventoryErrors:   make([]string, 0),
		RepositoryInventoryScope:    "dev-root-flat-org-one-level-and-worktrees",
	}
	entries, err := readBoundedDir(devDir, MaxDevScanEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to read dev directory %s: %w", devDir, err)
	}
	if len(entries) > MaxDevScanEntries {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryTruncated = true
		report.RepositoryInventoryErrors = append(report.RepositoryInventoryErrors, fmt.Sprintf("dev directory exceeds %d entries", MaxDevScanEntries))
	}

	for i := 0; i < len(entries) && i < MaxDevScanEntries; i++ {
		if ctx.Err() != nil {
			break
		}
		scanWorkstationEntry(ctx, devDir, entries[i], report)
	}
	if err := ctx.Err(); err != nil {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, "workstation scan canceled")
		return report, fmt.Errorf("workstation scan canceled: %w", err)
	}

	return report, nil
}

func readBoundedDir(path string, limit int) (entries []os.DirEntry, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, errors.New("scan path must be a directory, not a symlink")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	opened, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, errors.New("scan directory changed while opening")
	}
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := file.ReadDir(limit + 1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return entries, nil
}

func scanWorkstationEntry(ctx context.Context, devDir string, entry os.DirEntry, report *WorkstationReport) {
	if !entry.IsDir() {
		return
	}
	name := entry.Name()
	fullPath := filepath.Join(devDir, name)
	if strings.HasSuffix(name, "-worktrees") || name == "worktrees" {
		scanWorktreeDir(fullPath, report)
		scanRepositoryChildren(ctx, fullPath, name, report)
		return
	}
	if isIgnoredDevDir(name) {
		return
	}
	if _, err := os.Lstat(filepath.Join(fullPath, ".git")); err == nil {
		report.DevReposCount++
		inspectRepoGovernance(fullPath, name, report)
		appendRepositoryObservation(ctx, fullPath, report)
		return
	}
	if isBareRepository(fullPath) {
		report.DevReposCount++
		appendRepositoryObservation(ctx, fullPath, report)
		return
	}
	scanSubDir(ctx, fullPath, name, report)
}

func isIgnoredDevDir(name string) bool {
	switch name {
	case ".git", ".github", ".agents", ".gemini", ".claude", ".codex", "node_modules", "vendor", ".venv", ".cargo", "scratch", "build", "target", ".cache", "tmp":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func scanSubDir(ctx context.Context, parentPath, parentName string, report *WorkstationReport) {
	subEntries, err := readBoundedDir(parentPath, MaxDevScanEntries)
	if err != nil {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("read %s: %v", parentName, err))
		return
	}
	if len(subEntries) > MaxDevScanEntries {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryTruncated = true
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("%s exceeds %d entries", parentName, MaxDevScanEntries))
	}
	subCount := 0
	for _, sub := range subEntries {
		if subCount >= MaxDevScanEntries || ctx.Err() != nil {
			break
		}
		subCount++
		if !sub.IsDir() || isIgnoredDevDir(sub.Name()) {
			continue
		}
		subFullPath := filepath.Join(parentPath, sub.Name())
		gitPath := filepath.Join(subFullPath, ".git")
		if _, err := os.Lstat(gitPath); err == nil || isBareRepository(subFullPath) {
			report.DevReposCount++
			relPath := filepath.Join(parentName, sub.Name())
			inspectRepoGovernance(subFullPath, relPath, report)
			appendRepositoryObservation(ctx, subFullPath, report)
		}
	}
}

func scanRepositoryChildren(ctx context.Context, parentPath, parentName string, report *WorkstationReport) {
	entries, err := readBoundedDir(parentPath, MaxWorktreeScan)
	if err != nil {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("read %s: %v", parentName, err))
		return
	}
	if len(entries) > MaxWorktreeScan {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryTruncated = true
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("%s exceeds %d entries", parentName, MaxWorktreeScan))
	}
	for i := 0; i < len(entries) && i < MaxWorktreeScan; i++ {
		if ctx.Err() != nil {
			return
		}
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(parentPath, entry.Name())
		if _, err := os.Lstat(filepath.Join(path, ".git")); err != nil {
			continue
		}
		report.DevReposCount++
		appendRepositoryObservation(ctx, path, report)
	}
}

func isBareRepository(path string) bool {
	for _, name := range []string{"config", "HEAD"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			return false
		}
	}
	info, err := os.Stat(filepath.Join(path, "objects"))
	return err == nil && info.IsDir()
}

func appendBoundedError(errors []string, message string) []string {
	if len(errors) >= MaxDevScanEntries {
		return errors
	}
	return append(errors, message)
}

func appendRepositoryObservation(ctx context.Context, repoPath string, report *WorkstationReport) {
	if len(report.RepositoryObservations) >= MaxRepositoryObservations {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryTruncated = true
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("repository observation limit %d reached", MaxRepositoryObservations))
		return
	}
	observation := inspectRepository(ctx, repoPath)
	if len(observation.ProbeErrors) > 0 {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("repository probe incomplete for %s", observation.Path))
	}
	report.RepositoryObservations = append(report.RepositoryObservations, observation)
	if observation.DirtyState == "known" && observation.DirtyEntries > 0 {
		report.DirtyRepos = append(report.DirtyRepos, observation.Path)
	}
}

func inspectRepository(ctx context.Context, repoPath string) RepositoryObservation {
	path, err := filepath.Abs(repoPath)
	observation := RepositoryObservation{Path: filepath.Clean(repoPath), Classification: "unknown", RemoteState: "unknown", DirtyState: "unknown"}
	observation.WorkingdirPresent = util.PathExists(filepath.Join(repoPath, ".workingdir"))
	observation.WorkingdirTrackedState = "unknown"
	observation.DirtyScope = "unknown"
	if err == nil {
		observation.Path = filepath.Clean(path)
	}
	if err != nil {
		observation.ProbeErrors = append(observation.ProbeErrors, "canonical path unavailable")
		return observation
	}
	repoPath = path
	if info, statErr := os.Lstat(filepath.Join(repoPath, ".git")); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		observation.ProbeErrors = append(observation.ProbeErrors, "symlinked Git metadata is unsupported")
		return observation
	}
	inspectIdentity(ctx, repoPath, &observation)
	if observation.GitCommonDir == "" {
		return observation
	}
	inspectRepositoryState(ctx, repoPath, &observation)
	inspectRepositoryRemotes(ctx, repoPath, &observation)
	inspectRepositoryPrivacy(ctx, repoPath, &observation)
	return observation
}

func inspectIdentity(ctx context.Context, repoPath string, observation *RepositoryObservation) {
	common, commonErr := inventoryRunGit(ctx, repoPath, "rev-parse", "--git-common-dir")
	gitDir, gitDirErr := inventoryRunGit(ctx, repoPath, "rev-parse", "--git-dir")
	bare, bareErr := inventoryRunGit(ctx, repoPath, "rev-parse", "--is-bare-repository")
	if commonErr != nil || gitDirErr != nil || bareErr != nil {
		observation.ProbeErrors = append(observation.ProbeErrors, "git identity probe failed")
		return
	}
	if strings.TrimSpace(bare) != "true" {
		top, topErr := inventoryRunGit(ctx, repoPath, "rev-parse", "--show-toplevel")
		canonicalTop, canonicalErr := filepath.Abs(top)
		if topErr != nil || canonicalErr != nil || filepath.Clean(canonicalTop) != observation.Path {
			observation.ProbeErrors = append(observation.ProbeErrors, "git top-level identity mismatch")
			return
		}
	}
	observation.GitCommonDir = canonicalGitPath(repoPath, common)
	canonicalGitDir := canonicalGitPath(repoPath, gitDir)
	if strings.TrimSpace(bare) == "true" {
		observation.Classification = "bare"
	} else if canonicalGitDir != filepath.Join(observation.Path, ".git") {
		observation.Classification = "linked-worktree"
	} else {
		observation.Classification = "main"
	}
}

func inspectRepositoryState(ctx context.Context, repoPath string, observation *RepositoryObservation) {
	observation.DirtyScope = "checkout-excluding-submodules"
	if observation.Classification == "bare" {
		observation.DirtyState = "not-applicable"
		observation.DirtyScope = "not-applicable"
		return
	}
	filterCode, filterErr := inventoryRunGitExit(ctx, repoPath, "config", "--get-regexp", `^filter\..*\.(clean|process)$`)
	if filterErr != nil || filterCode == 0 {
		observation.ProbeErrors = append(observation.ProbeErrors, "git status unavailable: configured filters or filter probe failure")
		return
	}
	status, statusErr := inventoryRunGit(ctx, repoPath, "status", "--porcelain=v1", "--untracked-files=normal", "--ignore-submodules=all")
	if statusErr != nil {
		observation.ProbeErrors = append(observation.ProbeErrors, "git status probe failed")
		return
	}
	observation.DirtyState = "known"
	if strings.TrimSpace(status) != "" {
		observation.DirtyEntries = len(strings.Split(strings.TrimSpace(status), "\n"))
	}
}

func inspectRepositoryRemotes(ctx context.Context, repoPath string, observation *RepositoryObservation) {
	remotes, remoteErr := inventoryRunGit(ctx, repoPath, "remote", "-v")
	if remoteErr != nil {
		observation.ProbeErrors = append(observation.ProbeErrors, "git remote probe failed")
		return
	}
	var parsed bool
	observation.RemoteURLs, parsed = parseRemoteURLs(remotes, &observation.ProbeErrors)
	if !parsed {
		return
	}
	observation.RemoteState = "known"
	if observation.Classification == "main" && len(observation.RemoteURLs) == 0 {
		observation.Classification = "local-only"
	}
}

func inspectRepositoryPrivacy(ctx context.Context, repoPath string, observation *RepositoryObservation) {
	if observation.Classification == "bare" {
		observation.WorkingdirTrackedState = "not-applicable"
		return
	}
	tracked, trackedErr := inventoryRunGit(ctx, repoPath, "ls-files", "-z", "--", ".workingdir")
	if trackedErr == nil {
		observation.WorkingdirTrackedState = "known"
		observation.WorkingdirTrackedEntries = strings.Count(tracked, "\x00")
	} else {
		observation.ProbeErrors = append(observation.ProbeErrors, "workingdir tracked probe failed")
	}
	if observation.WorkingdirPresent {
		code, ignoreErr := inventoryRunGitExit(ctx, repoPath, "check-ignore", "--no-index", "-q", "--", ".workingdir/__praetor_privacy_probe__")
		if ignoreErr != nil || (code != 0 && code != 1) {
			observation.ProbeErrors = append(observation.ProbeErrors, "workingdir ignore probe failed")
		} else {
			ignored := code == 0
			observation.WorkingdirProbeIgnored = &ignored
		}
	}
}

func canonicalGitPath(repoPath, common string) string {
	if filepath.IsAbs(common) {
		return filepath.Clean(common)
	}
	return filepath.Clean(filepath.Join(repoPath, common))
}

// scanWorktreeDir records the worktrees under a *-worktrees container that have not been
// touched for DefaultStaleWorktreeAge. A worktree that is still being worked in is not
// stale: reporting every worktree as stale invites an operator or agent to delete live
// work, and contradicts the age criterion internal/gc uses when it actually prunes them.
//
// A container this scan cannot read, or one holding more entries than MaxWorktreeScan,
// yields a partial StaleWorktrees list. Such a scan marks the report incomplete and
// records why, so the partial list is never published as an exhaustive one.
func scanWorktreeDir(path string, report *WorkstationReport) {
	container := filepath.Base(path)
	subEntries, err := readBoundedDir(path, MaxWorktreeScan)
	if err != nil {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("stale worktree scan of %s failed: %v", container, err))
		return
	}
	if len(subEntries) > MaxWorktreeScan {
		report.RepositoryInventoryComplete = false
		report.RepositoryInventoryTruncated = true
		report.RepositoryInventoryErrors = appendBoundedError(report.RepositoryInventoryErrors, fmt.Sprintf("stale worktree scan of %s exceeds %d entries", container, MaxWorktreeScan))
	}
	for i := 0; i < len(subEntries) && i < MaxWorktreeScan; i++ {
		sub := subEntries[i]
		if !sub.IsDir() {
			continue
		}
		info, infoErr := sub.Info()
		if infoErr != nil {
			continue
		}
		if time.Since(info.ModTime()) <= DefaultStaleWorktreeAge {
			continue
		}
		report.StaleWorktrees = append(report.StaleWorktrees, filepath.Join(filepath.Base(path), sub.Name()))
	}
}

func inspectRepoGovernance(path, name string, report *WorkstationReport) {
	if len(report.RepositoryObservations) >= MaxRepositoryObservations {
		return
	}
	hasAgents := fileExists(filepath.Join(path, "AGENTS.md"))
	hasClaude := fileExists(filepath.Join(path, "CLAUDE.md"))
	hasWindsurf := fileExists(filepath.Join(path, ".windsurfrules"))
	hasGemini := fileExists(filepath.Join(path, ".gemini/GEMINI.md"))
	hasCodex := fileExists(filepath.Join(path, ".codex/rules.md"))

	if hasAgents {
		report.DiscoveredAgentDoc = append(report.DiscoveredAgentDoc, filepath.Join(name, "AGENTS.md"))
	}
	if hasClaude {
		report.DiscoveredAgentDoc = append(report.DiscoveredAgentDoc, filepath.Join(name, "CLAUDE.md"))
	}
	if hasCodex {
		report.DiscoveredAgentDoc = append(report.DiscoveredAgentDoc, filepath.Join(name, ".codex/rules.md"))
	}

	if !hasAgents && !hasClaude && !hasWindsurf && !hasGemini && !hasCodex {
		report.MissingRulesRepos = append(report.MissingRulesRepos, name)
	}
}

func fileExists(path string) bool {
	return util.PathExists(path)
}
