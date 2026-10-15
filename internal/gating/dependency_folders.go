// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxDependencyFolderDepth bounds how far below the repository root a dependency folder may sit (HISS-02).
const maxDependencyFolderDepth = 6

// maxDependencyFolders bounds how many dependency folders may be linked into the stage worktree (HISS-02).
const maxDependencyFolders = 64

// maxFingerprintEntries bounds the entries fingerprinted in one dependency folder (HISS-02).
const maxFingerprintEntries = 250000

// maxSearchIterations bounds how many folder visits candidate search may perform (HISS-02).
var maxSearchIterations = 100000

// symlinkFunc creates a symbolic link; injected in tests to exercise permission errors.
var symlinkFunc = os.Symlink

// dependencyFolderAllowList lists the base names of folders that may be linked into the stage worktree.
// It is an allow-list, never a deny-list.
var dependencyFolderAllowList = [...]string{"node_modules", ".venv", "venv"}

func isAllowedDependencyFolder(base string) bool {
	for i := 0; i < len(dependencyFolderAllowList); i++ {
		if base == dependencyFolderAllowList[i] {
			return true
		}
	}
	return false
}

type dirSearchItem struct {
	rel   string
	depth int
}

func isDirectoryEntry(baseDir string, d os.DirEntry) bool {
	if d.IsDir() {
		return true
	}
	if d.Type()&os.ModeSymlink != 0 {
		st, err := os.Stat(filepath.Join(baseDir, d.Name()))
		return err == nil && st.IsDir()
	}
	return false
}

func inspectCandidateEntry(stageWorktreeDir string, curr dirSearchItem, name string) (string, *dirSearchItem, error) {
	childDepth := curr.depth + 1
	childRel := name
	if curr.rel != "" {
		childRel = filepath.ToSlash(filepath.Join(curr.rel, name))
	}
	if childDepth > maxDependencyFolderDepth {
		return "", nil, nil
	}
	if isAllowedDependencyFolder(name) {
		if util.DirExists(filepath.Join(stageWorktreeDir, filepath.FromSlash(curr.rel))) {
			return childRel, nil, nil
		}
		return "", nil, nil
	}
	if util.DirExists(filepath.Join(stageWorktreeDir, filepath.FromSlash(childRel))) {
		return "", &dirSearchItem{rel: childRel, depth: childDepth}, nil
	}
	return "", nil, nil
}

func processSearchDir(stageWorktreeDir, fullDir string, curr dirSearchItem) ([]string, []dirSearchItem, error) {
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read directory %s: %w", curr.rel, err)
	}
	var candidates []string
	var nextQueue []dirSearchItem
	for _, d := range entries {
		if d.Name() == ".git" || !isDirectoryEntry(fullDir, d) {
			continue
		}
		cand, next, inspectErr := inspectCandidateEntry(stageWorktreeDir, curr, d.Name())
		if inspectErr != nil {
			return nil, nil, inspectErr
		}
		if cand != "" {
			candidates = append(candidates, cand)
		}
		if next != nil {
			nextQueue = append(nextQueue, *next)
		}
	}
	return candidates, nextQueue, nil
}

func searchCandidateFolders(ctx context.Context, checkoutDir, stageWorktreeDir string) ([]string, error) {
	queue := []dirSearchItem{{rel: "", depth: 0}}
	var candidates []string

	for iterations := 0; len(queue) > 0 && iterations < maxSearchIterations; iterations++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		curr := queue[0]
		queue = queue[1:]

		fullDir := filepath.Join(checkoutDir, filepath.FromSlash(curr.rel))
		found, next, err := processSearchDir(stageWorktreeDir, fullDir, curr)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, found...)
		queue = append(queue, next...)
	}
	if len(queue) > 0 {
		return nil, fmt.Errorf("dependency folder search exceeded bound of %d folder visits", maxSearchIterations)
	}
	return candidates, nil
}

func filterGitIgnoredFolders(ctx context.Context, checkoutDir string, candidates []string) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	ignored, err := util.GitIgnoredPaths(ctx, checkoutDir, candidates, false)
	if err != nil {
		return nil, fmt.Errorf("check git-ignored dependency folders: %w", err)
	}

	ignoredMap := make(map[string]bool, len(ignored))
	for _, p := range ignored {
		ignoredMap[p] = true
	}

	var matched []string
	for _, c := range candidates {
		if ignoredMap[c] || ignoredMap[c+"/"] {
			matched = append(matched, c)
		}
	}

	if len(matched) > maxDependencyFolders {
		return nil, fmt.Errorf("dependency folder count %d exceeded bound of %d", len(matched), maxDependencyFolders)
	}

	slices.Sort(matched)
	return matched, nil
}

// findDependencyFolders searches checkoutDir for git-ignored folders whose base name is on the
// allow-list and whose parent folder exists in stageWorktreeDir. The search does not descend below
// depth 6, bounds folder visits to maxSearchIterations (100,000), and limits linked folders to 64;
// exceeding the visit bound or folder count fails closed. Folders deeper than depth 6 are not linked.
func findDependencyFolders(ctx context.Context, checkoutDir, stageWorktreeDir string) ([]string, error) {
	candidates, err := searchCandidateFolders(ctx, checkoutDir, stageWorktreeDir)
	if err != nil {
		return nil, err
	}
	return filterGitIgnoredFolders(ctx, checkoutDir, candidates)
}

type folderEntryFingerprint struct {
	size  int64
	mtime time.Time
}

type folderFingerprint map[string]folderEntryFingerprint

// fingerprintFolder captures the relative path, size, and mtime of every entry within root.
func fingerprintFolder(root string) (folderFingerprint, error) {
	fp := make(folderFingerprint)
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		count++
		if count > maxFingerprintEntries {
			return fmt.Errorf("dependency folder %s exceeded fingerprint entry bound of %d", root, maxFingerprintEntries)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fp[filepath.ToSlash(rel)] = folderEntryFingerprint{
			size:  info.Size(),
			mtime: info.ModTime(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fp, nil
}

func fingerprintFolders(repoDir string, folders []string) (map[string]folderFingerprint, error) {
	fps := make(map[string]folderFingerprint, len(folders))
	for _, rel := range folders {
		fp, err := fingerprintFolder(filepath.Join(repoDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		fps[rel] = fp
	}
	return fps, nil
}

func verifyFolderFingerprints(repoDir string, folders []string, before map[string]folderFingerprint) error {
	for _, rel := range folders {
		afterFP, err := fingerprintFolder(filepath.Join(repoDir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if !fingerprintsEqual(before[rel], afterFP) {
			return fmt.Errorf("declared verification wrote into the checkout's dependencies in %s", rel)
		}
	}
	return nil
}

// fingerprintsEqual reports whether two folder fingerprints match exactly.
func fingerprintsEqual(a, b folderFingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for k, vA := range a {
		vB, ok := b[k]
		if !ok || vA.size != vB.size || !vA.mtime.Equal(vB.mtime) {
			return false
		}
	}
	return true
}

func cleanupSymlinks(created []string) error {
	var errs error
	for i := len(created) - 1; i >= 0; i-- {
		if err := os.Remove(created[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = errors.Join(errs, fmt.Errorf("remove dependency symlink %s: %w", created[i], err))
		}
	}
	return errs
}

func isSymlinkPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	if runtime.GOOS == "windows" {
		msg := strings.ToLower(err.Error())
		return strings.Contains(msg, "privilege") || strings.Contains(msg, "access is denied")
	}
	return false
}

func linkOneFolder(repoDir, stageWorktreeDir, rel string) (string, error) {
	absSrc, err := filepath.Abs(filepath.Join(repoDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("resolve dependency folder %s: %w", rel, err)
	}
	dst := filepath.Join(stageWorktreeDir, filepath.FromSlash(rel))
	if err := symlinkFunc(absSrc, dst); err != nil {
		if isSymlinkPermissionError(err) {
			return "", fmt.Errorf("link dependency folder %s into stage worktree: symlink creation requires developer mode or administrator rights on Windows (enable developer mode, or run make verify-all in the checkout): %w", rel, err)
		}
		return "", fmt.Errorf("link dependency folder %s into stage worktree: %w", rel, err)
	}
	return dst, nil
}

// linkDependencyFolders links each folder into stageWorktreeDir and prints a line naming them.
func linkDependencyFolders(repoDir, stageWorktreeDir string, folders []string) ([]string, func() error, error) {
	created := make([]string, 0, len(folders))
	for _, rel := range folders {
		dst, err := linkOneFolder(repoDir, stageWorktreeDir, rel)
		if err != nil {
			cleanErr := cleanupSymlinks(created)
			return nil, nil, errors.Join(err, cleanErr)
		}
		created = append(created, dst)
	}
	if len(folders) > 0 {
		fmt.Printf("linked dependency folders: %s\n", strings.Join(folders, ", "))
	}
	return created, func() error { return cleanupSymlinks(created) }, nil
}

// executeDeclaredVerify manages dependency linking, pre/post fingerprint checks, and make verify-all execution.
func executeDeclaredVerify(tCtx context.Context, cfg *stageConfig, dir string, bound time.Duration, origin string) (retErr error) {
	folders, findErr := findDependencyFolders(tCtx, cfg.repoDir, dir)
	if findErr != nil {
		return findErr
	}
	beforeFPs, fpErr := fingerprintFolders(cfg.repoDir, folders)
	if fpErr != nil {
		return fpErr
	}

	_, cleanup, linkErr := linkDependencyFolders(cfg.repoDir, dir, folders)
	if linkErr != nil {
		return linkErr
	}
	defer func() {
		if cleanErr := cleanup(); cleanErr != nil {
			retErr = errors.Join(retErr, cleanErr)
		}
	}()

	runCtx, envErr := util.WithCommandEnvironment(tCtx, util.InheritedEnvironment([]string{DeclaredVerifyGuardEnv + "=1"}))
	if envErr != nil {
		return fmt.Errorf("%s: %w", declaredVerifyCommand, envErr)
	}

	_, runErr := cfg.run(runCtx, dir, "make", declaredVerifyTarget)

	if diffErr := verifyFolderFingerprints(cfg.repoDir, folders, beforeFPs); diffErr != nil {
		return diffErr
	}

	if runErr != nil {
		return declaredVerifyFailure(tCtx, bound, origin, runErr)
	}
	return nil
}
