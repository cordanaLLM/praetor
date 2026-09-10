package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxDevScanEntries = 500
	MaxWorktreeScan   = 200
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
	DevReposCount      int      `json:"dev_repos_count"`
	StaleWorktrees     []string `json:"stale_worktrees"`
	MissingRulesRepos  []string `json:"missing_rules_repos"`
	DirtyRepos         []string `json:"dirty_repos"`
	DiscoveredAgentDoc []string `json:"discovered_agent_doc"`
}

// DetectArchetype recommends an archetype based on language and descriptions.
func DetectArchetype(lang, desc string) string {
	lowerDesc := strings.ToLower(desc)
	lowerLang := strings.ToLower(lang)

	if strings.Contains(lowerDesc, "gpu") || strings.Contains(lowerDesc, "vulkan") ||
		strings.Contains(lowerDesc, "ffmpeg") || strings.Contains(lowerDesc, "kernel") ||
		strings.Contains(lowerDesc, "sycl") || strings.Contains(lowerDesc, "cuda") {
		return "native-gpu-systems"
	}
	if strings.Contains(lowerDesc, "kubernetes") || strings.Contains(lowerDesc, "argocd") ||
		strings.Contains(lowerDesc, "terraform") || strings.Contains(lowerDesc, "gitops") ||
		strings.Contains(lowerDesc, "helm") {
		return "gitops-infra"
	}
	if strings.Contains(lowerDesc, "framework") || strings.Contains(lowerDesc, "composable") {
		return "framework"
	}
	if strings.Contains(lowerDesc, "client") || strings.Contains(lowerDesc, "sdk") ||
		strings.Contains(lowerDesc, "modules") {
		return "library-client"
	}
	if lowerLang == "astro" || strings.Contains(lowerDesc, "static") || strings.Contains(lowerDesc, "pages") {
		return "pages-site"
	}
	if lowerLang == "rust" || lowerLang == "go" || lowerLang == "python" || lowerLang == "typescript" {
		return "app-service"
	}
	return "template-seed"
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
		if strings.HasSuffix(name, "-worktrees") {
			scanWorktreeDir(fullPath, report)
			continue
		}

		// Inspect git repos
		gitPath := filepath.Join(fullPath, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			report.DevReposCount++
			inspectRepoGovernance(fullPath, name, report)
		}
	}

	return report, nil
}

func scanWorktreeDir(path string, report *WorkstationReport) {
	subEntries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	subCount := 0
	for _, sub := range subEntries {
		if subCount >= MaxWorktreeScan {
			break
		}
		subCount++
		if sub.IsDir() {
			report.StaleWorktrees = append(report.StaleWorktrees, filepath.Join(filepath.Base(path), sub.Name()))
		}
	}
}

func inspectRepoGovernance(path, name string, report *WorkstationReport) {
	hasAgents := fileExists(filepath.Join(path, "AGENTS.md"))
	hasClaude := fileExists(filepath.Join(path, "CLAUDE.md"))
	hasWindsurf := fileExists(filepath.Join(path, ".windsurfrules"))

	if hasAgents {
		report.DiscoveredAgentDoc = append(report.DiscoveredAgentDoc, filepath.Join(name, "AGENTS.md"))
	}
	if hasClaude {
		report.DiscoveredAgentDoc = append(report.DiscoveredAgentDoc, filepath.Join(name, "CLAUDE.md"))
	}

	if !hasAgents && !hasClaude && !hasWindsurf {
		report.MissingRulesRepos = append(report.MissingRulesRepos, name)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
