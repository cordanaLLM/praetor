// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// imageRefLinkOutside is the reference text of a scanned path whose link leaves the root.
const imageRefLinkOutside = "link outside the repository, not followed"

// RepositoryFiles asks git which paths belong to root: tracked files plus untracked files
// git is not ignoring.
func RepositoryFiles(ctx context.Context, root string) ([]string, error) {
	out, err := util.RunGitProbe(ctx, root, maxScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	parts := strings.Split(string(out.Stdout), "\x00")
	var files []string
	for i := 0; i < len(parts); i++ {
		rel := parts[i]
		if rel == "" {
			continue
		}
		if len(files) >= maxScannedFiles {
			return nil, fmt.Errorf("image pin scope passed %d files; narrow the listing", maxScannedFiles)
		}
		files = append(files, rel)
	}
	return files, nil
}

// imageRefCandidateFiles lists files under root via git when root is a git worktree, or
// via a bounded directory queue for fixture directories without .git.
func imageRefCandidateFiles(ctx context.Context, root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat image scan root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("image scan root %s is not a directory", root)
	}
	if _, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil {
		return RepositoryFiles(ctx, root)
	}
	return walkDirectoryFiles(root)
}

// walkDirectoryFiles enumerates relative file paths under root without recursion (HISS-01, HISS-02).
func walkDirectoryFiles(root string) ([]string, error) {
	walk := imageRefDirectoryWalk{dirs: []string{"."}}
	for d := 0; d < len(walk.dirs) && d < maxScannedFiles; d++ {
		relDir := walk.dirs[d]
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(relDir)))
		if err != nil {
			return nil, fmt.Errorf("read directory %s: %w", relDir, err)
		}
		if err := walk.appendEntries(relDir, entries); err != nil {
			return nil, err
		}
	}
	return walk.files, nil
}

type imageRefDirectoryWalk struct {
	dirs  []string
	files []string
}

func (walk *imageRefDirectoryWalk) appendEntries(relDir string, entries []os.DirEntry) error {
	for i := 0; i < len(entries) && i < maxScannedFiles; i++ {
		name := entries[i].Name()
		if name == ".git" || name == ".workingdir" || name == "node_modules" || name == "vendor" {
			continue
		}
		child := name
		if relDir != "." {
			child = relDir + "/" + name
		}
		if entries[i].IsDir() {
			walk.dirs = append(walk.dirs, child)
			continue
		}
		if len(walk.files) >= maxScannedFiles {
			return fmt.Errorf("image scan scope passed %d files", maxScannedFiles)
		}
		walk.files = append(walk.files, child)
	}
	return nil
}

// iterateScannedFiles executes a bounded iteration across repository candidate files matching
// filter, reading each file confined and limited, and invoking visit with the file content
// or read error (HISS-01, HISS-02, HISS-19).
func iterateScannedFiles(root string, files []string, filter func(string) bool, visit func(rel string, content []byte, readErr error) error) error {
	for i := 0; i < len(files) && i < maxScannedFiles; i++ {
		rel := files[i]
		if !filter(rel) {
			continue
		}
		content, readErr := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxImageRefFileBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if err := visit(rel, content, readErr); err != nil {
			return err
		}
	}
	return nil
}
