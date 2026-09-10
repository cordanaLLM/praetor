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

// DedupeReport summarizes removed duplicate skills and purged backups.
type DedupeReport struct {
	DryRun           bool     `json:"dry_run"`
	PrunedSkills     []string `json:"pruned_skills"`
	PurgedBackups    []string `json:"purged_backups"`
	ReclaimedEntries int      `json:"reclaimed_entries"`
	Errors           []string `json:"errors,omitempty"`
}

// DeduplicateSkills removes shadowed duplicate skills preferring gemini-config over gemini-root.
func DeduplicateSkills(ctx context.Context, report *SkillAuditReport, dryRun bool) (*DedupeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before skill deduplication: %w", err)
	}

	dedupeRep := &DedupeReport{
		DryRun:        dryRun,
		PrunedSkills:  make([]string, 0),
		PurgedBackups: make([]string, 0),
		Errors:        make([]string, 0),
	}

	count := 0
	for _, paths := range report.Duplicates {
		if count >= MaxSkillsScan {
			break
		}
		count++

		hasConfig := false
		var rootSkillDir string
		for _, p := range paths {
			if strings.Contains(p, "/config/skills/") {
				hasConfig = true
			} else if strings.Contains(p, "/.gemini/skills/") {
				rootSkillDir = filepath.Dir(p)
			}
		}

		if hasConfig && rootSkillDir != "" {
			dedupeRep.PrunedSkills = append(dedupeRep.PrunedSkills, rootSkillDir)
			if !dryRun {
				if err := os.RemoveAll(rootSkillDir); err != nil {
					dedupeRep.Errors = append(dedupeRep.Errors, fmt.Sprintf("failed removing %s: %v", rootSkillDir, err))
				}
			}
		}
	}

	dedupeRep.ReclaimedEntries = len(dedupeRep.PrunedSkills)
	return dedupeRep, nil
}

// PurgeBackups purges stale GEMINI.md backup files from the gemini directory.
func PurgeBackups(ctx context.Context, geminiDir string, backups []string, dryRun bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before purging backups: %w", err)
	}

	purged := make([]string, 0, len(backups))
	count := 0
	for _, b := range backups {
		if count >= MaxSkillsScan {
			break
		}
		count++

		target := filepath.Join(geminiDir, b)
		purged = append(purged, target)
		if !dryRun {
			_ = os.Remove(target)
		}
	}

	return purged, nil
}
