package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxSkillsScan = 500

// SkillInfo captures discovered skill properties and metadata.
type SkillInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
}

// SkillAuditReport details discovered skills and duplicates.
type SkillAuditReport struct {
	TotalSkills  int                 `json:"total_skills"`
	UniqueSkills int                 `json:"unique_skills"`
	Duplicates   map[string][]string `json:"duplicates"`
	StaleBackups []string            `json:"stale_backups"`
}

// AuditSkills scans the provided directories for skill definitions and redundancy.
func AuditSkills(ctx context.Context, geminiDir, repoSkillsDir string) (*SkillAuditReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before skill audit: %w", err)
	}

	report := &SkillAuditReport{
		Duplicates:   make(map[string][]string),
		StaleBackups: make([]string, 0),
	}

	skillPaths := make(map[string][]string)

	// Scan global gemini skills
	scanSkillDir(filepath.Join(geminiDir, "skills"), "gemini-root", skillPaths)
	scanSkillDir(filepath.Join(geminiDir, "config", "skills"), "gemini-config", skillPaths)

	// Scan repo skills
	if repoSkillsDir != "" {
		scanSkillDir(repoSkillsDir, "repo-local", skillPaths)
	}

	// Calculate counts and duplicates
	report.UniqueSkills = len(skillPaths)
	for name, paths := range skillPaths {
		report.TotalSkills += len(paths)
		if len(paths) > 1 {
			report.Duplicates[name] = paths
		}
	}

	// Scan stale GEMINI.md backups
	scanGeminiBackups(geminiDir, report)

	return report, nil
}

func scanSkillDir(dir, origin string, registry map[string][]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	count := 0
	for _, entry := range entries {
		if count >= MaxSkillsScan {
			break
		}
		count++

		if !entry.IsDir() {
			continue
		}

		skillName := entry.Name()
		skillFile := filepath.Join(dir, skillName, "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			registry[skillName] = append(registry[skillName], skillFile)
		}
	}
}

func scanGeminiBackups(geminiDir string, report *SkillAuditReport) {
	entries, err := os.ReadDir(geminiDir)
	if err != nil {
		return
	}

	count := 0
	for _, entry := range entries {
		if count >= MaxSkillsScan {
			break
		}
		count++

		name := entry.Name()
		if strings.HasPrefix(name, "GEMINI.md.bak-") {
			report.StaleBackups = append(report.StaleBackups, name)
		}
	}
}
