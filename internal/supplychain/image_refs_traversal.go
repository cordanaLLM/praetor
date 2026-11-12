// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// imageRefLinkOutside is the reference text of a scanned path whose link leaves the root.
const imageRefLinkOutside = "link outside the repository, not followed"

// imageRefScanTimeout bounds the overall duration of an image reference repository scan (HISS-02).
// The repository pin scan measured 1.49s plain and 13.81s under -race; one minute
// leaves over 4x race-mode headroom while maxScannedFiles and maxImageRefFileBytes
// bound the work and imageRefIOTimeout still bounds each read.
const imageRefScanTimeout = time.Minute

// imageRefIOTimeout bounds individual directory or file read operations during image reference scanning (HISS-02).
const imageRefIOTimeout = 5 * time.Second

// maxScanDirs bounds subdirectories queued during a non-git directory walk (HISS-02).
const maxScanDirs = 8192

// maxScanDirEntries bounds directory entries read from one directory during a non-git walk (HISS-02).
const maxScanDirEntries = 8192

// RepositoryFiles asks git which paths belong to root (tracked files plus untracked files
// git is not ignoring) and returns those matching keep, bounded by maxScannedFiles.
func RepositoryFiles(ctx context.Context, root string, keep func(string) bool) ([]string, error) {
	out, err := util.RunGitProbe(ctx, root, maxScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	parts := strings.Split(string(out.Stdout), "\x00")
	var files []string
	for i := 0; i < len(parts) && i <= maxScopeBytes; i++ {
		rel := parts[i]
		if rel == "" || (keep != nil && !keep(rel)) {
			continue
		}
		if len(files) >= maxScannedFiles {
			return nil, fmt.Errorf("image pin scope passed %d scanned candidate files; narrow the listing", maxScannedFiles)
		}
		files = append(files, rel)
	}
	return files, nil
}

// imageRefCandidateFiles lists candidate files matching keep under root via git when root is a
// git worktree, or via a bounded directory queue for fixture directories without .git.
func imageRefCandidateFiles(ctx context.Context, root string, keep func(string) bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, imageRefScanTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image scan context: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat image scan root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("image scan root %s is not a directory", root)
	}
	if _, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil {
		return RepositoryFiles(ctx, root, keep)
	}
	return walkDirectoryFiles(ctx, root, keep)
}

// walkDirectoryFiles enumerates relative file paths matching keep under root without recursion (HISS-01, HISS-02).
func walkDirectoryFiles(ctx context.Context, root string, keep func(string) bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, imageRefScanTimeout)
	defer cancel()
	walk := imageRefDirectoryWalk{dirs: []string{"."}, keep: keep}
	var d int
	for d = 0; d < len(walk.dirs) && d < maxScanDirs; d++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("walk directory context: %w", err)
		}
		relDir := walk.dirs[d]
		entries, err := readScanDirectory(ctx, root, relDir)
		if err != nil {
			return nil, fmt.Errorf("read directory %s: %w", relDir, err)
		}
		if err := walk.appendEntries(relDir, entries); err != nil {
			return nil, err
		}
	}
	if d < len(walk.dirs) {
		return nil, fmt.Errorf("image scan scope passed %d directories", maxScanDirs)
	}
	return walk.files, nil
}

type imageRefDirectoryWalk struct {
	dirs  []string
	files []string
	keep  func(string) bool
}

// readScanDirectory reads up to maxScanDirEntries+1 directory entries under root/relDir,
// bounding memory allocation and I/O deadline before appendEntries enforces the limit (HISS-02).
func readScanDirectory(ctx context.Context, root, relDir string) (entries []os.DirEntry, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, imageRefIOTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("directory read context: %w", err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, r.Close()) }()
	sub := filepath.FromSlash(relDir)
	if sub == "" {
		sub = "."
	}
	dir, err := r.Open(sub)
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	if err := setDirectoryDeadline(ctx, dir); err != nil {
		return nil, err
	}
	entries, err = dir.ReadDir(maxScanDirEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read directory entries: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("directory read context: %w", err)
	}
	if len(entries) <= maxScanDirEntries {
		slices.SortFunc(entries, func(a, b os.DirEntry) int {
			return cmp.Compare(a.Name(), b.Name())
		})
	}
	return entries, nil
}

// setDirectoryDeadline applies the context deadline to an open directory handle when supported.
func setDirectoryDeadline(ctx context.Context, dir *os.File) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	if err := dir.SetDeadline(deadline); err != nil && !errors.Is(err, os.ErrNoDeadline) {
		return fmt.Errorf("set directory deadline: %w", err)
	}
	return nil
}

// isIgnoredScanDir reports whether a directory name should be skipped during image reference scanning.
func isIgnoredScanDir(name string) bool {
	return name == ".git" || name == ".workingdir" || name == "node_modules" || name == "vendor"
}

// appendEntries filters and appends subdirectories and candidate files to the walk state, enforcing safety limits.
func (walk *imageRefDirectoryWalk) appendEntries(relDir string, entries []os.DirEntry) error {
	if len(entries) > maxScanDirEntries {
		return fmt.Errorf("directory %s passed %d entries", relDir, maxScanDirEntries)
	}
	for i := 0; i < len(entries) && i < maxScanDirEntries; i++ {
		if err := walk.appendEntry(relDir, entries[i]); err != nil {
			return err
		}
	}
	return nil
}

// appendEntry classifies one directory entry into walk.dirs or walk.files when kept.
func (walk *imageRefDirectoryWalk) appendEntry(relDir string, entry os.DirEntry) error {
	name := entry.Name()
	if isIgnoredScanDir(name) {
		return nil
	}
	child := name
	if relDir != "." {
		child = relDir + "/" + name
	}
	if entry.IsDir() {
		if len(walk.dirs) >= maxScanDirs {
			return fmt.Errorf("image scan scope passed %d directories", maxScanDirs)
		}
		walk.dirs = append(walk.dirs, child)
		return nil
	}
	if walk.keep != nil && !walk.keep(child) {
		return nil
	}
	if len(walk.files) >= maxScannedFiles {
		return fmt.Errorf("image scan scope passed %d scanned candidate files; narrow the listing", maxScannedFiles)
	}
	walk.files = append(walk.files, child)
	return nil
}

// iterateScannedFiles executes a bounded iteration across repository candidate files matching
// filter, reading each file confined and limited under ctx, and invoking visit with the file
// content or read error (HISS-01, HISS-02, HISS-19).
func iterateScannedFiles(ctx context.Context, root string, files []string, filter func(string) bool, visit func(rel string, content []byte, readErr error) error) error {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, imageRefScanTimeout)
	defer cancel()
	if len(files) > maxScannedFiles {
		return fmt.Errorf("image scan scope passed %d scanned candidate files; narrow the listing", maxScannedFiles)
	}
	for i := 0; i < len(files) && i < maxScannedFiles; i++ {
		// The caller's Done closes before cancellation reaches its children.
		if err := cmp.Or(callerCtx.Err(), ctx.Err()); err != nil {
			return fmt.Errorf("scan files context: %w", err)
		}
		rel := files[i]
		if filter != nil && !filter(rel) {
			continue
		}
		content, readErr := readScannedFile(ctx, root, rel)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
			return readErr
		}
		if err := visit(rel, content, readErr); err != nil {
			return err
		}
	}
	return nil
}

// readScannedFile reads one candidate file under root with a bounded I/O context timeout (HISS-02).
func readScannedFile(ctx context.Context, root, rel string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, imageRefIOTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read %s context: %w", rel, err)
	}
	content, err := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxImageRefFileBytes)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("read %s context: %w", rel, ctxErr)
	}
	return content, err
}
