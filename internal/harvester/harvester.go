package harvester

import (
	"context"
	"fmt"
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
	// stale. It matches gc.DefaultMaxWorktreeAge, which is what actually prunes them.
	DefaultStaleWorktreeAge = 24 * time.Hour
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
	StaleWorktrees     []string `json:"stale_worktrees"`
	MissingRulesRepos  []string `json:"missing_rules_repos"`
	DirtyRepos         []string `json:"dirty_repos"`
	DiscoveredAgentDoc []string `json:"discovered_agent_doc"`
}

// ScanLocalWorkstation audits directories under devDir for repos and worktrees.
func ScanLocalWorkstation(ctx context.Context, devDir string) (*WorkstationReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before scan: %w", err)
	}

	entries, err := os.ReadDir(devDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read dev directory %s: %w", devDir, err)
	}

	report := &WorkstationReport{
		StaleWorktrees:     make([]string, 0),
		MissingRulesRepos:  make([]string, 0),
		DirtyRepos:         make([]string, 0),
		DiscoveredAgentDoc: make([]string, 0),
	}

	count := 0
	for _, entry := range entries {
		if count >= MaxDevScanEntries {
			break
		}
		count++

		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		fullPath := filepath.Join(devDir, name)

		// Check for worktree container directories
		if strings.HasSuffix(name, "-worktrees") || name == "worktrees" {
			scanWorktreeDir(fullPath, report)
			continue
		}

		if isIgnoredDevDir(name) {
			continue
		}

		// Inspect git repos (flat layout)
		gitPath := filepath.Join(fullPath, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			report.DevReposCount++
			inspectRepoGovernance(fullPath, name, report)
			continue
		}

		// Inspect nested git repos (org/repo or bucket/repo layout)
		scanSubDir(fullPath, name, report)
	}

	return report, nil
}

func isIgnoredDevDir(name string) bool {
	switch name {
	case ".git", ".github", ".agents", ".gemini", ".claude", ".codex", "node_modules", "vendor", ".venv", ".cargo", "scratch", "build", "target", ".cache", "tmp":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func scanSubDir(parentPath, parentName string, report *WorkstationReport) {
	subEntries, err := os.ReadDir(parentPath)
	if err != nil {
		return
	}
	subCount := 0
	for _, sub := range subEntries {
		if subCount >= MaxDevScanEntries {
			break
		}
		subCount++
		if !sub.IsDir() || isIgnoredDevDir(sub.Name()) {
			continue
		}
		subFullPath := filepath.Join(parentPath, sub.Name())
		gitPath := filepath.Join(subFullPath, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			report.DevReposCount++
			relPath := filepath.Join(parentName, sub.Name())
			inspectRepoGovernance(subFullPath, relPath, report)
		}
	}
}

// scanWorktreeDir records the worktrees under a *-worktrees container that have not been
// touched for DefaultStaleWorktreeAge. A worktree that is still being worked in is not
// stale: reporting every worktree as stale invites an operator or agent to delete live
// work, and contradicts the age criterion internal/gc uses when it actually prunes them.
func scanWorktreeDir(path string, report *WorkstationReport) {
	subEntries, err := os.ReadDir(path)
	if err != nil {
		return
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
