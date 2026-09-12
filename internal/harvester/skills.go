package harvester

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxSkillsScan bounds every skill directory iteration (HISS-02).
const MaxSkillsScan = 500

// Skill root origin labels. The origin, not a substring of the path, decides what
// DeduplicateSkills may consider; canonical paths and real files authorize deletion.
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

// skillRemoval pairs one root skill with its canonical config copy in the same home.
type skillRemoval struct {
	gemini string
	name   string
}

// canonicalDeletionInfo rejects links in every component of an existing target.
func canonicalDeletionInfo(target string, directory bool) (os.FileInfo, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	if resolved != absolute {
		return nil, fmt.Errorf("deletion path contains symlink: %s", target)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("unexpected deletion target type: %s", target)
	}
	return info, nil
}

func validSkillName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

func canonicalGeminiSkill(name string, location SkillLocation) (string, error) {
	if !filepath.IsAbs(location.Path) || filepath.Clean(location.Path) != location.Path {
		return "", fmt.Errorf("skill path must be absolute and normalized: %s", location.Path)
	}
	if !validSkillName(name) {
		return "", fmt.Errorf("invalid skill name: %q", name)
	}
	gemini := filepath.Dir(filepath.Dir(filepath.Dir(location.Path)))
	if location.Origin == OriginGeminiConfig {
		gemini = filepath.Dir(gemini)
	}
	parent := filepath.Join(gemini, "skills", name)
	if location.Origin == OriginGeminiConfig {
		parent = filepath.Join(gemini, "config", "skills", name)
	}
	if filepath.Base(gemini) != ".gemini" || location.Path != filepath.Join(parent, "SKILL.md") {
		return "", fmt.Errorf("skill origin does not match canonical Gemini path: %s", location.Path)
	}
	if _, err := canonicalDeletionInfo(location.Path, false); err != nil {
		return "", err
	}
	return gemini, nil
}

func skillGroupRemovals(name string, locations []SkillLocation) ([]skillRemoval, error) {
	if len(locations) > MaxSkillsScan {
		return nil, fmt.Errorf("skill location count exceeds %d", MaxSkillsScan)
	}
	roots, configs := map[string]bool{}, map[string]bool{}
	for _, location := range locations {
		if location.Origin != OriginGeminiRoot && location.Origin != OriginGeminiConfig {
			continue
		}
		gemini, err := canonicalGeminiSkill(name, location)
		if err != nil {
			return nil, err
		}
		if location.Origin == OriginGeminiRoot {
			roots[gemini] = true
		} else {
			configs[gemini] = true
		}
	}
	if len(configs) == 0 {
		return nil, nil
	}
	homes := make([]string, 0, len(roots))
	for home := range roots {
		homes = append(homes, home)
	}
	sort.Strings(homes)
	removals := make([]skillRemoval, 0, len(homes))
	for _, home := range homes {
		if !configs[home] {
			return nil, fmt.Errorf("skill %s has no config counterpart in %s", name, home)
		}
		removals = append(removals, skillRemoval{gemini: home, name: name})
	}
	return removals, nil
}

func preflightSkillRemovals(ctx context.Context, report *SkillAuditReport) ([]skillRemoval, error) {
	if report == nil {
		return nil, fmt.Errorf("skill audit report is required for deduplication")
	}
	if len(report.Duplicates) > MaxSkillsScan {
		return nil, fmt.Errorf("duplicate skill count exceeds %d", MaxSkillsScan)
	}
	names := make([]string, 0, len(report.Duplicates))
	for name := range report.Duplicates {
		names = append(names, name)
	}
	sort.Strings(names)
	removals := make([]skillRemoval, 0)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		group, err := skillGroupRemovals(name, report.Duplicates[name])
		if err != nil {
			return nil, err
		}
		if len(removals)+len(group) > MaxSkillsScan {
			return nil, fmt.Errorf("skill removal count exceeds %d", MaxSkillsScan)
		}
		removals = append(removals, group...)
	}
	return removals, nil
}

// openDeletionRoot pins a validated directory without changing its permissions.
func openDeletionRoot(directory string) (*os.Root, error) {
	info, err := canonicalDeletionInfo(directory, true)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = fmt.Errorf("deletion root changed while opening: %s", directory)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func removeSkill(ctx context.Context, candidate skillRemoval) (bool, error) {
	locations := []SkillLocation{
		{Path: filepath.Join(candidate.gemini, "skills", candidate.name, "SKILL.md"), Origin: OriginGeminiRoot},
		{Path: filepath.Join(candidate.gemini, "config", "skills", candidate.name, "SKILL.md"), Origin: OriginGeminiConfig},
	}
	if _, err := skillGroupRemovals(candidate.name, locations); err != nil {
		return false, err
	}
	root, err := openDeletionRoot(filepath.Join(candidate.gemini, "skills"))
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, errors.Join(err, root.Close())
	}
	removeErr := root.RemoveAll(candidate.name)
	return removeErr == nil, errors.Join(removeErr, root.Close())
}

// DeduplicateSkills validates the full plan before removing canonical Gemini shadows.
// Dry runs list planned paths with zero reclaimed entries; errors preserve live progress.
func DeduplicateSkills(ctx context.Context, report *SkillAuditReport, dryRun bool) (*DedupeReport, error) {
	result := &DedupeReport{DryRun: dryRun, PrunedSkills: []string{}, PurgedBackups: []string{}, Errors: []string{}}
	if err := ctx.Err(); err != nil {
		return dedupeFailure(result, err)
	}
	removals, err := preflightSkillRemovals(ctx, report)
	if err != nil {
		return dedupeFailure(result, err)
	}
	for _, candidate := range removals {
		if err := ctx.Err(); err != nil {
			return dedupeFailure(result, err)
		}
		target := filepath.Join(candidate.gemini, "skills", candidate.name)
		if dryRun {
			result.PrunedSkills = append(result.PrunedSkills, target)
			continue
		}
		removed, err := removeSkill(ctx, candidate)
		if removed {
			result.PrunedSkills = append(result.PrunedSkills, target)
			result.ReclaimedEntries++
		}
		if err != nil {
			return dedupeFailure(result, fmt.Errorf("remove skill %s: %w", target, err))
		}
	}
	return result, nil
}

func dedupeFailure(result *DedupeReport, err error) (*DedupeReport, error) {
	result.Errors = append(result.Errors, err.Error())
	return result, err
}

func validateBackup(root *os.Root, name string) (bool, error) {
	if !strings.HasPrefix(name, "GEMINI.md.bak-") || len(name) == len("GEMINI.md.bak-") ||
		filepath.Base(name) != name || filepath.IsAbs(name) || strings.ContainsAny(name, "/\\") {
		return false, fmt.Errorf("invalid Gemini backup name: %q", name)
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("backup is not a regular file: %s", name)
	}
	return true, nil
}

func preflightBackups(root *os.Root, backups []string) ([]string, error) {
	seen := make(map[string]bool)
	for _, name := range backups {
		exists, err := validateBackup(root, name)
		if err != nil {
			return nil, err
		}
		if exists {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// PurgeBackups validates every basename before mutations and returns actual partial progress.
func PurgeBackups(ctx context.Context, geminiDir string, backups []string, dryRun bool) (purged []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(backups) > MaxSkillsScan {
		return nil, fmt.Errorf("backup count exceeds %d", MaxSkillsScan)
	}
	root, err := openDeletionRoot(geminiDir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := preflightBackups(root, backups)
	if err != nil {
		return nil, err
	}
	purged = make([]string, 0, len(names))
	for _, name := range names {
		removed, err := purgeBackup(ctx, root, name, dryRun)
		if err != nil {
			return purged, err
		}
		if removed {
			purged = append(purged, filepath.Join(geminiDir, name))
		}
	}
	return purged, nil
}

func purgeBackup(ctx context.Context, root *os.Root, name string, dryRun bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	exists, err := validateBackup(root, name)
	if err != nil || !exists {
		return false, err
	}
	if dryRun {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := root.Remove(name); err != nil {
		return false, fmt.Errorf("remove backup %s: %w", name, err)
	}
	return true, nil
}
