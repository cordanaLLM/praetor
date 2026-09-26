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

// sourceFiles lists the Go source this repository owns.
//
// testdata is excluded on both scope paths, the git listing and the directory walk, so the
// two agree; util.IsGoNonTestSource is the one rule, shared with the DevContainer bootstrap
// capture. Go itself never builds testdata, and a fixture corpus is deliberately
// repetitive: a rule needing a tested and an untested copy of the same function must contain
// two near-identical files, so reporting them as clones reports the evidence as the defect.
func sourceFiles(ctx context.Context, repoPath string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("dedupe scan requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gitRoot, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return nil, err
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
func gitSourceFiles(ctx context.Context, repoPath string) ([]string, error) {
	out, err := util.RunGitProbe(ctx, repoPath, maxGitScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("enumerate dedupe Git scope: %w", err)
	}
	entries := bytes.Split(out.Stdout, []byte{0})
	if len(entries) > maxScopeEntries+1 {
		return nil, fmt.Errorf("dedupe Git scope exceeds %d entries", maxScopeEntries)
	}
	var files []string
	for _, entry := range entries {
		path := string(entry)
		if !util.IsGoNonTestSource(path) {
			continue
		}
		if !filepath.IsLocal(path) {
			return nil, fmt.Errorf("nonlocal dedupe source: %q", path)
		}
		_, err := os.Lstat(filepath.Join(repoPath, path))
		if errors.Is(err, os.ErrNotExist) {
			continue // Tracked deletions are absent from the working tree being scanned.
		}
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

func directorySourceFiles(ctx context.Context, repoPath string) ([]string, error) {
	var files []string
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
		if !util.IsGoNonTestSource(path) {
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}
