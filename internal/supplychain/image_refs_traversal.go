// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// imageRefLinkOutside is the reference text of a scanned path whose link leaves the root.
const imageRefLinkOutside = "link outside the repository, not followed"

// imageRefScanTimeout bounds the overall duration of an image reference repository scan (HISS-02).
// TestScanRepositoryPinsContention enforces a one-fifth scan budget with two Go
// execution threads; BenchmarkScanRepositoryPins records listing, read and
// extraction costs. File-count, byte and per-read bounds remain independent.
const imageRefScanTimeout = time.Minute

// imageRefIOTimeout bounds individual directory or file read operations during image reference scanning (HISS-02).
const imageRefIOTimeout = 5 * time.Second

// maxScanDirs bounds subdirectories queued during a non-git directory walk (HISS-02).
const maxScanDirs = 8192

// maxScanDirEntries bounds directory entries read from one directory during a non-git walk (HISS-02).
const maxScanDirEntries = 8192

// RepositoryFiles asks git which paths belong to root (tracked files plus untracked files
// git is not ignoring) and streams NUL-delimited entries through keep, bounded by
// maxScannedFiles candidates, maxScopeListingEntries streamed entries, and maxScopeBytes retained bytes.
func RepositoryFiles(ctx context.Context, root string, keep func(string) bool) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("enumerate image pin scope requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	probeCtx, err := util.WithGitProbeEnvironment(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	scanCtx, cancelScan := context.WithTimeout(probeCtx, imageRefScanTimeout)
	defer cancelScan()
	streamCtx, abortStream := context.WithCancel(scanCtx)
	defer abortStream()

	writer := &scopeListingStream{
		callerCtx: ctx,
		scanCtx:   scanCtx,
		abort:     abortStream,
		keep:      keep,
	}
	stderrCap := min(max(maxScopeBytes, 1), util.MaxCommandOutputBytes)
	stderr, runErr := util.RunCommandStream(
		streamCtx, root, "git", nil, writer, stderrCap,
		util.GitProbeArgs("ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")...,
	)
	if writer.streamErr != nil {
		return nil, writer.streamErr
	}
	if runErr != nil {
		return nil, formatScopeStreamError(root, ctx, scanCtx, runErr, stderr)
	}
	if err := writer.finish(); err != nil {
		return nil, err
	}
	return writer.files, nil
}

// formatScopeStreamError names why the image pin scope listing failed: a context error first, else git's stderr preview, else the run error.
func formatScopeStreamError(root string, callerCtx, scanCtx context.Context, runErr error, stderr []byte) error {
	if ctxErr := cmp.Or(callerCtx.Err(), scanCtx.Err()); ctxErr != nil {
		return fmt.Errorf("enumerate image pin scope in %s: %w", root, ctxErr)
	}
	if preview := util.BodyPreview(stderr); preview != "" {
		return fmt.Errorf("enumerate image pin scope in %s: %w: %s", root, runErr, preview)
	}
	return fmt.Errorf("enumerate image pin scope in %s: %w", root, runErr)
}

type scopeListingStream struct {
	callerCtx   context.Context
	scanCtx     context.Context
	abort       context.CancelFunc
	keep        func(string) bool
	tail        bytes.Buffer
	files       []string
	seenEntries int
	keptBytes   int
	streamErr   error
}

// Write splits a chunk of the NUL-delimited scope listing into paths and handles each complete one.
func (s *scopeListingStream) Write(p []byte) (int, error) {
	if s.streamErr != nil {
		return 0, s.streamErr
	}
	if err := s.activeContextErr(); err != nil {
		return 0, s.abortWith(fmt.Errorf("enumerate image pin scope: %w", err))
	}
	total := len(p)
	maxSteps := len(p) + 1
	for steps := 0; len(p) > 0 && steps < maxSteps; steps++ {
		idx := bytes.IndexByte(p, 0)
		if idx < 0 {
			if err := s.appendTail(p); err != nil {
				return 0, err
			}
			return total, nil
		}
		if err := s.handleSegment(p[:idx]); err != nil {
			return 0, err
		}
		p = p[idx+1:]
	}
	if len(p) > 0 {
		return 0, s.abortWith(fmt.Errorf("image pin scope chunk passed %d parse steps", maxSteps))
	}
	return total, nil
}

// activeContextErr returns the caller's or the scan's context error, if either is set.
func (s *scopeListingStream) activeContextErr() error {
	return cmp.Or(s.callerCtx.Err(), s.scanCtx.Err())
}

// abortWith records the first stream error and stops the git process.
func (s *scopeListingStream) abortWith(err error) error {
	if s.streamErr == nil {
		s.streamErr = err
		s.abort()
	}
	return s.streamErr
}

// appendTail keeps an incomplete trailing path for the next chunk, within maxScopeBytes.
func (s *scopeListingStream) appendTail(rem []byte) error {
	if s.keptBytes+s.tail.Len()+len(rem) > maxScopeBytes {
		return s.abortWith(fmt.Errorf("image pin scope passed maxScopeBytes (%d bytes)", maxScopeBytes))
	}
	_, err := s.tail.Write(rem)
	if err != nil {
		return s.abortWith(err)
	}
	return nil
}

// handleSegment checks the context and records one complete path from the listing.
func (s *scopeListingStream) handleSegment(seg []byte) error {
	if err := s.activeContextErr(); err != nil {
		return s.abortWith(fmt.Errorf("enumerate image pin scope: %w", err))
	}
	if s.keptBytes+s.tail.Len()+len(seg) > maxScopeBytes {
		return s.abortWith(fmt.Errorf("image pin scope passed maxScopeBytes (%d bytes)", maxScopeBytes))
	}
	rel := string(seg)
	if s.tail.Len() > 0 {
		if _, err := s.tail.Write(seg); err != nil {
			return s.abortWith(err)
		}
		rel = s.tail.String()
		s.tail.Reset()
	}
	if rel == "" {
		return nil
	}
	return s.recordPath(rel)
}

// recordPath counts one entry against maxScopeListingEntries and keeps it when keep accepts it, up to maxScannedFiles.
func (s *scopeListingStream) recordPath(rel string) error {
	s.seenEntries++
	if s.seenEntries >= maxScopeListingEntries {
		return s.abortWith(fmt.Errorf("image pin scope reached maxScopeListingEntries bound (%d entries)", maxScopeListingEntries))
	}
	if s.keep != nil && !s.keep(rel) {
		return s.checkPostKeepContext()
	}
	if err := s.checkPostKeepContext(); err != nil {
		return err
	}
	if len(s.files) >= maxScannedFiles {
		return s.abortWith(fmt.Errorf("image pin scope passed %d scanned candidate files; narrow the listing", maxScannedFiles))
	}
	s.files = append(s.files, rel)
	s.keptBytes += len(rel)
	return nil
}

// checkPostKeepContext stops the stream when the context ended while keep ran.
func (s *scopeListingStream) checkPostKeepContext() error {
	if err := s.activeContextErr(); err != nil {
		return s.abortWith(fmt.Errorf("enumerate image pin scope: %w", err))
	}
	return nil
}

// finish checks the context and handles a final path git did not terminate with NUL.
func (s *scopeListingStream) finish() error {
	if err := s.activeContextErr(); err != nil {
		return s.abortWith(fmt.Errorf("enumerate image pin scope: %w", err))
	}
	if s.tail.Len() == 0 {
		return nil
	}
	trailing := s.tail.String()
	s.tail.Reset()
	return s.recordPath(trailing)
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
	return readScannedFileUsing(ctx, root, rel, util.ReadConfinedLimited)
}

// readScannedFileUsing keeps the bounded read injectable for slow-reader deadline tests.
func readScannedFileUsing(ctx context.Context, root, rel string, read func(string, string, int64) ([]byte, error)) ([]byte, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, imageRefIOTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read %s context: %w", rel, err)
	}
	content, err := read(root, filepath.FromSlash(rel), maxImageRefFileBytes)
	if ctxErr := cmp.Or(callerCtx.Err(), ctx.Err()); ctxErr != nil {
		return nil, fmt.Errorf("read %s context: %w", rel, ctxErr)
	}
	return content, err
}
