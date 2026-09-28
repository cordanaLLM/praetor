package dedupe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxScopeEntries  = 200000
	maxGitScopeBytes = 8 << 20
)

// goLanguage is util.SourceLanguage's name for Go, the one language this detector reads.
const goLanguage = "go"

// sourceScope is the scan's inventory: the Go sources it reads and, per language, how many
// source files of the repository it cannot read. The count is what lets a report say that a
// verdict over eight Go files says nothing about eighty Rust files beside them (#161).
type sourceScope struct {
	goFiles   []string
	unscanned map[string]int
}

// sourceLanguageOf classifies one repository-relative path: goLanguage for Go production
// source (util.IsGoNonTestSource), another util.SourceLanguage name for source this detector
// cannot read, "" for everything else. Go's test surface (util.IsGoTestSurface: _test.go
// files and testdata trees) is "" in every language, so a fixture corpus is neither scanned
// nor reported.
func sourceLanguageOf(path string) string {
	if util.IsGoNonTestSource(path) {
		return goLanguage
	}
	if util.IsGoTestSurface(path) {
		return ""
	}
	if language := util.SourceLanguage(path); language != goLanguage {
		return language
	}
	return ""
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
	slices.Sort(scope.goFiles)
	scope.goFiles = slices.Compact(scope.goFiles)
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
		if entry.IsDir() {
			if path != repoPath && shouldSkipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if language := sourceLanguageOf(rel); language != "" {
			scope.add(language, rel)
		}
		return nil
	})
	return scope, err
}
