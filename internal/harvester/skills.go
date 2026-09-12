package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxSkillsScan bounds every skill directory iteration (HISS-02).
const MaxSkillsScan = 500

// Skill root origin labels. The origin, not a substring of the path, decides what
// DeduplicateSkills is allowed to delete.
const (
	OriginCopilot      = "copilot"
	OriginCodex        = "codex"
	OriginClaude       = "claude"
	OriginGeminiRoot   = "gemini-root"
	OriginGeminiConfig = "gemini-config"
	OriginUniversal    = "universal"
	OriginDirectRoot   = "direct-root"
	OriginDirectConfig = "direct-config"
	OriginRepoLocal    = "repo-local"
)

// SkillInfo captures discovered skill properties and metadata.
type SkillInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
}

// SkillLocation is one discovered copy of a skill: the SKILL.md path and the labelled root
// it was found under.
type SkillLocation struct {
	Path   string `json:"path"`
	Origin string `json:"origin"`
}

// SkillAuditReport details discovered skills and duplicates.
type SkillAuditReport struct {
	TotalSkills  int                        `json:"total_skills"`
	UniqueSkills int                        `json:"unique_skills"`
	Duplicates   map[string][]SkillLocation `json:"duplicates"`
	StaleBackups []string                   `json:"stale_backups"`
}

// skillRoot is a directory scanned for skills plus the origin label recorded for it.
type skillRoot struct {
	dir    string
	origin string
}

// isGeminiDir reports whether baseDir already points at a .gemini directory.
func isGeminiDir(baseDir string) bool {
	return filepath.Base(filepath.Clean(baseDir)) == ".gemini"
}

// absOrClean resolves dir to an absolute path, falling back to the cleaned form.
func absOrClean(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return abs
}

// dedupeSkillRoots removes roots that resolve to the same directory, keeping the first
// (most specifically labelled) occurrence. When baseDir is itself ~/.gemini the "direct-*"
// roots are the very same directories as the "gemini-*" roots; scanning both would count
// every Gemini skill twice and report it as a duplicate of itself.
func dedupeSkillRoots(roots []skillRoot) []skillRoot {
	seen := make(map[string]bool, len(roots))
	unique := make([]skillRoot, 0, len(roots))
	for i := 0; i < len(roots) && i < MaxSkillsScan; i++ {
		resolved := absOrClean(roots[i].dir)
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		unique = append(unique, skillRoot{dir: resolved, origin: roots[i].origin})
	}
	return unique
}

// agentSkillRoots returns the deduplicated list of skill roots to scan for baseDir.
func agentSkillRoots(baseDir, repoSkillsDir string) []skillRoot {
	home := baseDir
	if isGeminiDir(baseDir) {
		home = filepath.Dir(filepath.Clean(baseDir))
	}

	roots := []skillRoot{
		{filepath.Join(home, ".copilot", "skills"), OriginCopilot},
		{filepath.Join(home, ".codex", "skills"), OriginCodex},
		{filepath.Join(home, ".claude", "skills"), OriginClaude},
		{filepath.Join(home, ".gemini", "skills"), OriginGeminiRoot},
		{filepath.Join(home, ".gemini", "config", "skills"), OriginGeminiConfig},
		{filepath.Join(home, ".agents", "skills"), OriginUniversal},
		{filepath.Join(baseDir, "skills"), OriginDirectRoot},
		{filepath.Join(baseDir, "config", "skills"), OriginDirectConfig},
	}
	if repoSkillsDir != "" {
		roots = append(roots, skillRoot{repoSkillsDir, OriginRepoLocal})
	}
	return dedupeSkillRoots(roots)
}

// AuditSkills scans all agent skill roots across Copilot, Codex, Claude, Gemini, Universal
// and (optionally) a repository-local skills directory.
func AuditSkills(ctx context.Context, baseDir, repoSkillsDir string) (*SkillAuditReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before skill audit: %w", err)
	}

	report := &SkillAuditReport{
		Duplicates:   make(map[string][]SkillLocation),
		StaleBackups: make([]string, 0),
	}

	registry := make(map[string][]SkillLocation)
	for _, root := range agentSkillRoots(baseDir, repoSkillsDir) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during skill audit: %w", err)
		}
		scanSkillDir(root, registry)
	}

	report.UniqueSkills = len(registry)
	for name, locations := range registry {
		report.TotalSkills += len(locations)
		if len(locations) > 1 {
			report.Duplicates[name] = locations
		}
	}

	scanGeminiBackups(baseDir, report)
	return report, nil
}

// scanSkillDir registers every <dir>/<name>/SKILL.md under the root's origin label.
func scanSkillDir(root skillRoot, registry map[string][]SkillLocation) {
	entries, err := os.ReadDir(root.dir)
	if err != nil {
		return
	}

	for i := 0; i < len(entries) && i < MaxSkillsScan; i++ {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(root.dir, entry.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillFile); statErr != nil {
			continue
		}
		registry[entry.Name()] = append(registry[entry.Name()], SkillLocation{Path: skillFile, Origin: root.origin})
	}
}

// scanGeminiBackups collects stale GEMINI.md backup files in the gemini directory.
func scanGeminiBackups(geminiDir string, report *SkillAuditReport) {
	entries, err := os.ReadDir(geminiDir)
	if err != nil {
		return
	}

	for i := 0; i < len(entries) && i < MaxSkillsScan; i++ {
		if strings.HasPrefix(entries[i].Name(), "GEMINI.md.bak-") {
			report.StaleBackups = append(report.StaleBackups, entries[i].Name())
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

// shadowedGeminiRootDirs returns the ~/.gemini/skills directories of a duplicate group that
// are shadowed by a ~/.gemini/config/skills copy of the same name. Only the Gemini root is
// ever a prune candidate: a same-named skill under ~/.claude, ~/.codex, ~/.copilot,
// ~/.agents or a repository working tree belongs to another agent and is not a shadow.
func shadowedGeminiRootDirs(locations []SkillLocation) []string {
	hasConfig := false
	candidates := make([]string, 0, len(locations))
	for i := 0; i < len(locations) && i < MaxSkillsScan; i++ {
		switch locations[i].Origin {
		case OriginGeminiConfig, OriginDirectConfig:
			hasConfig = true
		case OriginGeminiRoot, OriginDirectRoot:
			candidates = append(candidates, filepath.Dir(locations[i].Path))
		}
	}
	if !hasConfig {
		return nil
	}
	return candidates
}

// DeduplicateSkills removes skill directories under ~/.gemini/skills that are shadowed by a
// ~/.gemini/config/skills copy of the same name. It never touches another agent's skill
// root or a repository working tree.
func DeduplicateSkills(ctx context.Context, report *SkillAuditReport, dryRun bool) (*DedupeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before skill deduplication: %w", err)
	}
	if report == nil {
		return nil, fmt.Errorf("skill audit report is required for deduplication")
	}

	dedupeRep := &DedupeReport{
		DryRun:        dryRun,
		PrunedSkills:  make([]string, 0),
		PurgedBackups: make([]string, 0),
		Errors:        make([]string, 0),
	}

	count := 0
	for _, locations := range report.Duplicates {
		if count >= MaxSkillsScan {
			break
		}
		count++
		pruneShadowedSkills(shadowedGeminiRootDirs(locations), dryRun, dedupeRep)
	}

	dedupeRep.ReclaimedEntries = len(dedupeRep.PrunedSkills)
	return dedupeRep, nil
}

// pruneShadowedSkills records and (outside dry-run mode) removes the shadowed directories.
func pruneShadowedSkills(dirs []string, dryRun bool, dedupeRep *DedupeReport) {
	for i := 0; i < len(dirs) && i < MaxSkillsScan; i++ {
		dedupeRep.PrunedSkills = append(dedupeRep.PrunedSkills, dirs[i])
		if dryRun {
			continue
		}
		if err := os.RemoveAll(dirs[i]); err != nil {
			dedupeRep.Errors = append(dedupeRep.Errors, fmt.Sprintf("failed removing %s: %v", dirs[i], err))
		}
	}
}

// PurgeBackups purges stale GEMINI.md backup files from the gemini directory.
func PurgeBackups(ctx context.Context, geminiDir string, backups []string, dryRun bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before purging backups: %w", err)
	}

	purged := make([]string, 0, len(backups))
	for i := 0; i < len(backups) && i < MaxSkillsScan; i++ {
		target := filepath.Join(geminiDir, backups[i])
		purged = append(purged, target)
		if dryRun {
			continue
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove backup %s: %w", target, err)
		}
	}

	return purged, nil
}
