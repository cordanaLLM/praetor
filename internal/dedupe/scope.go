package dedupe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxScopeEntries  = 200000
	maxGitScopeBytes = 8 << 20
)

// goLanguage is util.SourceLanguage's name for Go, the one language this detector reads.
const goLanguage = "go"

// sourceScope is the scan's inventory: the Go sources it reads, the foreign sources it
// delegates to jscpd, and, per language, how many source files of the repository it
// found. The count is what lets a report say that a verdict over eight Go files says nothing
// about eighty Rust files beside them (#161).
type sourceScope struct {
	goFiles      []string
	foreignFiles []string
	unscanned    map[string]int
}

func isWorkflowYAML(path string) bool {
	clean := filepath.ToSlash(path)
	ext := strings.ToLower(filepath.Ext(clean))
	if ext != ".yml" && ext != ".yaml" {
		return false
	}
	return strings.HasPrefix(clean, ".github/workflows/") ||
		strings.Contains(clean, "/.github/workflows/") ||
		strings.HasPrefix(clean, "workflows/") ||
		strings.Contains(clean, "/workflows/") ||
		strings.HasSuffix(clean, "workflow.yaml") ||
		strings.HasSuffix(clean, "workflow.yml")
}

// sourceLanguageOf classifies one repository-relative path: goLanguage for Go production
// source (util.IsGoNonTestSource), "yaml" for workflow definitions, another util.SourceLanguage
// name for source this detector cannot read natively, "" for everything else. Go's test surface
// (util.IsGoTestSurface: _test.go files and testdata trees) is "" in every language, so a
// fixture corpus is neither scanned nor reported.
func sourceLanguageOf(path string) string {
	if util.IsGoNonTestSource(path) {
		return goLanguage
	}
	if util.IsGoTestSurface(path) {
		return ""
	}
	if isWorkflowYAML(path) {
		return "yaml"
	}
	if language := util.SourceLanguage(path); language != goLanguage {
		return language
	}
	return ""
}

// SourceLanguageCounts counts the source files of the repository at repoPath per
// util.SourceLanguage name, over the inventory the dedupe scan reads (sourceFiles): Go
// production source under "go", every other language's source under its own name, and the
// Go test surface (_test.go files, testdata trees) nowhere. The pre-migration epic names the
// languages no needs analyzer detects from it, so the epic and the dedupe scan's Not Scanned
// line count the same files.
func SourceLanguageCounts(ctx context.Context, repoPath string) (map[string]int, error) {
	scope, err := sourceFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(scope.unscanned)+1)
	maps.Copy(counts, scope.unscanned)
	if len(scope.goFiles) > 0 {
		counts[goLanguage] = len(scope.goFiles)
	}
	return counts, nil
}

// LanguageFileCounts renders per-language file counts as "shell (9 files), zig (1 file)",
// in sorted language order so the line is the same on every run. The dedupe scan's Not
// Scanned line and the pre-migration epic's unanalyzed languages share it. No counts render
// as "".
func LanguageFileCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, language := range slices.Sorted(maps.Keys(counts)) {
		unit := "files"
		if counts[language] == 1 {
			unit = "file"
		}
		parts = append(parts, fmt.Sprintf("%s (%d %s)", language, counts[language], unit))
	}
	return strings.Join(parts, ", ")
}

// add records path under language, as sourceLanguageOf classified it.
func (s *sourceScope) add(language, path string) {
	if language == goLanguage {
		s.goFiles = append(s.goFiles, path)
		return
	}
	if s.unscanned == nil {
		s.unscanned = make(map[string]int)
	}
	s.unscanned[language]++
	s.foreignFiles = append(s.foreignFiles, path)
}

// sourceFiles inventories the source this repository owns.
//
// testdata is excluded on both scope paths, the git listing and the directory walk, so the
// two agree; util.IsGoNonTestSource is the one rule, shared with the DevContainer bootstrap
// capture. Go itself never builds testdata, and a fixture corpus is deliberately
// repetitive: a rule needing a tested and an untested copy of the same function must contain
// two near-identical files, so reporting them as clones reports the evidence as the defect.
func sourceFiles(ctx context.Context, repoPath string) (sourceScope, error) {
	if ctx == nil {
		return sourceScope{}, errors.New("dedupe scan requires a context")
	}
	if err := ctx.Err(); err != nil {
		return sourceScope{}, err
	}
	gitRoot, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return sourceScope{}, err
	}
	if gitRoot {
		return gitSourceFiles(ctx, repoPath)
	}
	return directorySourceFiles(ctx, repoPath)
}

// gitSourceFiles enumerates the scanned repository's own sources through the hardened
// probe. A dedupe scan inspects a repository Praetor does not own, so it must not let that
// repository decide what runs: a plain inventory call honours the scanned tree's
// core.fsmonitor and core.hooksPath, which executed a repository-configured program during
// praetorctl dedupe scan. util.RunGitProbe disables both and scrubs the inherited
// environment; internal/hiss enumerates the same way for the same reason.
func gitSourceFiles(ctx context.Context, repoPath string) (sourceScope, error) {
	out, err := util.RunGitProbe(ctx, repoPath, maxGitScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return sourceScope{}, fmt.Errorf("enumerate dedupe Git scope: %w", err)
	}
	entries := bytes.Split(out.Stdout, []byte{0})
	if len(entries) > maxScopeEntries+1 {
		return sourceScope{}, fmt.Errorf("dedupe Git scope exceeds %d entries", maxScopeEntries)
	}
	var scope sourceScope
	for _, entry := range entries {
		path := string(entry)
		if path == "" || hiss.ShouldIgnorePath(filepath.ToSlash(path)) {
			continue
		}
		language := sourceLanguageOf(path)
		if language == "" {
			continue
		}
		present, err := worktreeFilePresent(repoPath, path)
		if err != nil {
			return sourceScope{}, err
		}
		if present {
			scope.add(language, path)
		}
	}
	finalizeScope(&scope)
	return scope, nil
}

// worktreeFilePresent reports whether a path Git listed is in the working tree being
// scanned. Tracked deletions are listed but absent, and are skipped rather than failed.
func worktreeFilePresent(repoPath, path string) (bool, error) {
	if !filepath.IsLocal(path) {
		return false, fmt.Errorf("nonlocal dedupe source: %q", path)
	}
	_, err := os.Lstat(filepath.Join(repoPath, path))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func finalizeScope(scope *sourceScope) {
	slices.Sort(scope.goFiles)
	scope.goFiles = slices.Compact(scope.goFiles)
	slices.Sort(scope.foreignFiles)
	scope.foreignFiles = slices.Compact(scope.foreignFiles)
}

func processDirEntry(repoPath, path string, entry fs.DirEntry, scope *sourceScope) error {
	rel, err := filepath.Rel(repoPath, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if entry.IsDir() {
		if path != repoPath && hiss.ShouldIgnoreDir(entry.Name(), rel) {
			return filepath.SkipDir
		}
		return nil
	}
	if hiss.ShouldIgnorePath(rel) {
		return nil
	}
	if language := sourceLanguageOf(rel); language != "" {
		scope.add(language, rel)
	}
	return nil
}

func directorySourceFiles(ctx context.Context, repoPath string) (sourceScope, error) {
	var scope sourceScope
	visited := 0
	err := filepath.WalkDir(repoPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > maxScopeEntries {
			return fmt.Errorf("dedupe directory scope exceeds %d entries", maxScopeEntries)
		}
		return processDirEntry(repoPath, path, entry, &scope)
	})
	if err == nil {
		finalizeScope(&scope)
	}
	return scope, err
}
