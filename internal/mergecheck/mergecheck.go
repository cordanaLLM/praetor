// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package mergecheck inspects merge commits to ensure first-parent changes
// were not dropped in favor of whole-file resolutions from second parents.
// Order: engine-dropped-hunk-gate-20261027.
package mergecheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// DefaultMaxMerges bounds the number of merge commits inspected per range (HISS-02).
	DefaultMaxMerges = 1000
	// maxDiffFiles bounds the number of changed files inspected per parent pair (HISS-02).
	maxDiffFiles = 10000
	// maxParents bounds the number of parents inspected for an octopus merge (HISS-02).
	maxParents = 32
	// gitOpTimeout bounds each individual git operation (HISS-02).
	gitOpTimeout = 30 * time.Second
	// maxBlobBytes bounds the size of a single blob read (HISS-02).
	maxBlobBytes = 16 << 20
	// binaryProbeBytes bounds the prefix inspected for NUL bytes.
	binaryProbeBytes = 8000
	// maxLsTreeArgBytes bounds the path argument bytes per git ls-tree call (HISS-02, HISS-21).
	maxLsTreeArgBytes = 16 << 10
	// maxMergeBaseBytes bounds git merge-base output (HISS-02; SHA-256 is 65 bytes with newline).
	maxMergeBaseBytes = 128
)

// Finding records one file whose changes were dropped in a merge commit.
type Finding struct {
	MergeSHA      string
	FilePath      string
	Parent1       string
	Parent2       string
	Deleted       bool
	Binary        bool
	DroppedParent int
}

// Result records the outcome of inspecting merge commits in a revision range.
type Result struct {
	Range       string
	MergesCount int
	Findings    []Finding
	Notices     []string
}

// Options configures merge check execution.
type Options struct {
	RepoDir   string
	Range     string
	MaxMerges int
}

type commitBlobs struct {
	repoDir  string
	mergeSHA string
	p1SHA    string
	p2SHA    string
	m        map[string]string
	p1       map[string]string
	p2       map[string]string
	base     map[string]string
}

type fileHunkContext struct {
	repoDir  string
	mergeSHA string
	p1       string
	p2       string
	file     string
	blobM    string
	blobP1   string
	blobP2   string
	blobBase string
	okBase   bool
}

// Check inspects merge commits in range for dropped first-parent work.
// Order: engine-dropped-hunk-gate-20261027.
func Check(ctx context.Context, opts Options) (*Result, error) {
	if ctx == nil {
		return nil, errors.New("mergecheck: context must not be nil")
	}
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	commitRange := strings.TrimSpace(opts.Range)
	if commitRange == "" {
		resolved, err := ResolveDefaultRange(ctx, repoDir)
		if err != nil {
			return nil, fmt.Errorf("mergecheck: resolve default range: %w", err)
		}
		commitRange = resolved
	}
	maxMerges := opts.MaxMerges
	if maxMerges <= 0 {
		maxMerges = DefaultMaxMerges
	}
	return runMergeCheck(ctx, repoDir, commitRange, maxMerges)
}

// runMergeCheck inspects merge commits within commitRange up to maxMerges.
func runMergeCheck(ctx context.Context, repoDir, commitRange string, maxMerges int) (*Result, error) {
	merges, err := listMergeCommits(ctx, repoDir, commitRange, maxMerges)
	if err != nil {
		return nil, err
	}
	res := &Result{
		Range:       commitRange,
		MergesCount: len(merges),
		Findings:    nil,
		Notices:     nil,
	}
	if len(merges) == 0 {
		return res, nil
	}
	for i := 0; i < len(merges) && i < maxMerges; i++ {
		findings, notices, err := checkMergeCommit(ctx, repoDir, merges[i])
		if err != nil {
			return nil, err
		}
		if len(findings) > 0 {
			res.Findings = append(res.Findings, findings...)
		}
		if len(notices) > 0 {
			res.Notices = append(res.Notices, notices...)
		}
	}
	return res, nil
}

// ResolveDefaultRange resolves the default range (origin default branch..HEAD).
// Order: engine-dropped-hunk-gate-20261027.
func ResolveDefaultRange(ctx context.Context, repoDir string) (string, error) {
	if before := strings.TrimSpace(os.Getenv("GITHUB_EVENT_BEFORE")); !isZeroOID(before) {
		if commit, err := util.ResolveGitCommit(ctx, repoDir, before); err == nil && commit != "" {
			return before + "..HEAD", nil
		}
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, repoDir, nil)
	if err != nil {
		return "", fmt.Errorf("resolve default branch: %w", err)
	}
	if branch == "" {
		branch = forge.FallbackDefaultBranch
	}
	remoteRef := "refs/remotes/origin/" + branch
	if commit, err := util.ResolveGitCommit(ctx, repoDir, remoteRef); err == nil && commit != "" {
		return "origin/" + branch + "..HEAD", nil
	}
	localRef := "refs/heads/" + branch
	if commit, err := util.ResolveGitCommit(ctx, repoDir, localRef); err == nil && commit != "" {
		return branch + "..HEAD", nil
	}
	return "origin/" + branch + "..HEAD", nil
}

// isZeroOID reports whether the given object ID is empty or all zeroes.
func isZeroOID(oid string) bool {
	return oid == "" || strings.Trim(oid, "0") == ""
}

// FormatFinding formats one finding line naming merge sha12, file, and both parents.
// Order: engine-dropped-hunk-gate-20261027.
func FormatFinding(f Finding) string {
	mSha12 := shortSHA(f.MergeSHA)
	p1Sha12 := shortSHA(f.Parent1)
	p2Sha12 := shortSHA(f.Parent2)
	if f.Deleted {
		parent := "either-parent"
		switch f.DroppedParent {
		case 1:
			parent = "first-parent"
		case 2:
			parent = "second-parent"
		}
		return fmt.Sprintf("[FAIL] merge %s: %s dropped %s changes (deleted in merge; parents %s, %s)",
			mSha12, f.FilePath, parent, p1Sha12, p2Sha12)
	}
	if f.Binary {
		return fmt.Sprintf("[FAIL] merge %s: %s dropped first-parent changes (binary comparison; parents %s, %s)",
			mSha12, f.FilePath, p1Sha12, p2Sha12)
	}
	return fmt.Sprintf("[FAIL] merge %s: %s dropped first-parent changes (parents %s, %s)",
		mSha12, f.FilePath, p1Sha12, p2Sha12)
}

// shortSHA returns the first 12 characters of a commit hash or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

// checkMergeCommit inspects all parent pairs of a merge commit for dropped hunks.
func checkMergeCommit(ctx context.Context, repoDir, sha string) ([]Finding, []string, error) {
	parents, err := commitParents(ctx, repoDir, sha)
	if err != nil {
		return nil, nil, fmt.Errorf("read parents for %s: %w", shortSHA(sha), err)
	}
	if len(parents) < 2 {
		return nil, nil, nil
	}
	if len(parents) > maxParents {
		return nil, nil, fmt.Errorf("mergecheck: merge %s has %d parents, exceeds limit %d", shortSHA(sha), len(parents), maxParents)
	}
	p1 := parents[0]
	var allFindings []Finding
	var allNotices []string
	seenNoBase := false
	for i := 1; i < len(parents) && i < maxParents; i++ {
		findings, noBase, err := checkParentPair(ctx, repoDir, sha, p1, parents[i])
		if err != nil {
			return nil, nil, err
		}
		if noBase && !seenNoBase {
			seenNoBase = true
			allNotices = append(allNotices, fmt.Sprintf("merge %s: no merge base, not judged", shortSHA(sha)))
		}
		if len(findings) > 0 {
			allFindings = append(allFindings, findings...)
		}
	}
	return allFindings, allNotices, nil
}

// checkParentPair verifies that mergeSHA preserved changes made between base and p1 against p2.
func checkParentPair(ctx context.Context, repoDir, mergeSHA, p1, p2 string) ([]Finding, bool, error) {
	base, err := mergeBase(ctx, repoDir, p1, p2)
	if err != nil {
		return nil, false, fmt.Errorf("find merge-base between %s and %s: %w", shortSHA(p1), shortSHA(p2), err)
	}
	if base == "" {
		return nil, true, nil
	}
	files, err := filesChanged(ctx, repoDir, base, p1)
	if err != nil {
		return nil, false, fmt.Errorf("diff %s %s: %w", shortSHA(base), shortSHA(p1), err)
	}
	if len(files) == 0 {
		return nil, false, nil
	}
	blobs, err := loadBlobsForFiles(ctx, repoDir, mergeSHA, p1, p2, base, files)
	if err != nil {
		return nil, false, err
	}
	var findings []Finding
	for i := 0; i < len(files) && i < maxDiffFiles; i++ {
		finding, err := checkFileHunk(ctx, files[i], blobs)
		if err != nil {
			return nil, false, fmt.Errorf("merge %s: file %s: %w", shortSHA(mergeSHA), files[i], err)
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
	}
	return findings, false, nil
}

// loadBlobsForFiles fetches blob object IDs for files across merge, parents, and base commits.
func loadBlobsForFiles(ctx context.Context, repoDir, m, p1, p2, base string, files []string) (*commitBlobs, error) {
	bm, err := treeBlobs(ctx, repoDir, m, files)
	if err != nil {
		return nil, fmt.Errorf("read tree for %s: %w", shortSHA(m), err)
	}
	bp1, err := treeBlobs(ctx, repoDir, p1, files)
	if err != nil {
		return nil, fmt.Errorf("read tree for %s: %w", shortSHA(p1), err)
	}
	bp2, err := treeBlobs(ctx, repoDir, p2, files)
	if err != nil {
		return nil, fmt.Errorf("read tree for %s: %w", shortSHA(p2), err)
	}
	bBase, err := treeBlobs(ctx, repoDir, base, files)
	if err != nil {
		return nil, fmt.Errorf("read tree for %s: %w", shortSHA(base), err)
	}
	return &commitBlobs{
		repoDir:  repoDir,
		mergeSHA: m,
		p1SHA:    p1,
		p2SHA:    p2,
		m:        bm,
		p1:       bp1,
		p2:       bp2,
		base:     bBase,
	}, nil
}

// checkFileHunk evaluates whether a single file dropped changes in the merge commit.
func checkFileHunk(ctx context.Context, file string, blobs *commitBlobs) (*Finding, error) {
	if blobs == nil {
		return nil, nil
	}
	blobM, okM := blobs.m[file]
	blobP1, okP1 := blobs.p1[file]
	blobP2, okP2 := blobs.p2[file]
	blobBase, okBase := blobs.base[file]

	if parent, ok := droppedByDeletion(okM, okP1, okP2, okBase, blobP2, blobBase); ok {
		return &Finding{
			MergeSHA:      blobs.mergeSHA,
			FilePath:      file,
			Parent1:       blobs.p1SHA,
			Parent2:       blobs.p2SHA,
			Deleted:       true,
			DroppedParent: parent,
		}, nil
	}
	if !okM || !okP1 || !okP2 || blobM == blobP1 || blobP2 == blobP1 {
		return nil, nil
	}
	return judgeDroppedHunk(ctx, fileHunkContext{
		repoDir:  blobs.repoDir,
		mergeSHA: blobs.mergeSHA,
		p1:       blobs.p1SHA,
		p2:       blobs.p2SHA,
		file:     file,
		blobM:    blobM,
		blobP1:   blobP1,
		blobP2:   blobP2,
		blobBase: blobBase,
		okBase:   okBase,
	})
}

// droppedByDeletion reports which parent's modifications were dropped when a file is absent in the merge.
func droppedByDeletion(okM, okP1, okP2, okBase bool, blobP2, blobBase string) (int, bool) {
	if okM {
		return 0, false
	}
	p2Mod := okP2 && (!okBase || blobP2 != blobBase)
	if okP1 && p2Mod {
		return 0, true
	}
	if okP1 {
		return 1, true
	}
	if p2Mod {
		return 2, true
	}
	return 0, false
}

// judgeDroppedHunk applies three-way merge reconstruction to determine if a hunk was dropped.
func judgeDroppedHunk(ctx context.Context, h fileHunkContext) (*Finding, error) {
	mBytes, p1Bytes, p2Bytes, baseBytes, err := readHunkBlobs(ctx, h)
	if err != nil {
		return nil, err
	}
	if anyBinaryBlob(mBytes, p1Bytes, p2Bytes, baseBytes) {
		return binaryDropped(h), nil
	}
	reconstructed, bin, err := mergeFileBlobs(ctx, h.repoDir, "--theirs", p1Bytes, baseBytes, p2Bytes)
	if err != nil {
		return nil, err
	}
	if bin {
		return binaryDropped(h), nil
	}
	if bytes.Equal(reconstructed, p2Bytes) {
		return nil, nil
	}
	applied, bin, err := mergeFileBlobs(ctx, h.repoDir, "--ours", mBytes, p2Bytes, reconstructed)
	if err != nil {
		return nil, err
	}
	if bin {
		return binaryDropped(h), nil
	}
	if bytes.Equal(applied, mBytes) {
		return nil, nil
	}
	return &Finding{MergeSHA: h.mergeSHA, FilePath: h.file, Parent1: h.p1, Parent2: h.p2, Binary: false, DroppedParent: 1}, nil
}

// readHunkBlobs reads the merge's, the first parent's, the second parent's and, when the merge base holds the
// file, the base's blob; a file the base lacks reads as empty.
func readHunkBlobs(ctx context.Context, h fileHunkContext) (m, p1, p2, base []byte, err error) {
	if m, err = readBlob(ctx, h.repoDir, h.blobM); err != nil {
		return nil, nil, nil, nil, err
	}
	if p1, err = readBlob(ctx, h.repoDir, h.blobP1); err != nil {
		return nil, nil, nil, nil, err
	}
	if p2, err = readBlob(ctx, h.repoDir, h.blobP2); err != nil {
		return nil, nil, nil, nil, err
	}
	if h.okBase {
		if base, err = readBlob(ctx, h.repoDir, h.blobBase); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return m, p1, p2, base, nil
}

// binaryDropped judges a binary file the merge took whole from the second parent. A binary merges
// by taking one side, so when both parents changed it the choice is the resolution, not a dropped
// hunk. Only a second parent that left the file as the merge base had it turns the take into a
// revert of the first parent's change.
func binaryDropped(h fileHunkContext) *Finding {
	if h.blobM != h.blobP2 || !h.okBase || h.blobP2 != h.blobBase {
		return nil
	}
	return &Finding{MergeSHA: h.mergeSHA, FilePath: h.file, Parent1: h.p1, Parent2: h.p2, Binary: true, DroppedParent: 1}
}

// mergeFileBlobs executes git merge-file between three byte buffers in a temporary directory.
func mergeFileBlobs(ctx context.Context, repoDir, conflictFlag string, currentBytes, baseBytes, otherBytes []byte) (merged []byte, isBin bool, err error) {
	tmpDir, err := os.MkdirTemp("", "mergecheck-blob-*")
	if err != nil {
		return nil, false, fmt.Errorf("create temp dir for merge-file: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tmpDir)) }()

	currentFile := filepath.Join(tmpDir, "current")
	baseFile := filepath.Join(tmpDir, "base")
	otherFile := filepath.Join(tmpDir, "other")

	if err := os.WriteFile(currentFile, currentBytes, 0o600); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(baseFile, baseBytes, 0o600); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(otherFile, otherBytes, 0o600); err != nil {
		return nil, false, err
	}

	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()

	cmdBytes, runErr := util.RunGitProbeWithin(opCtx, repoDir, maxBlobBytes, gitOpTimeout,
		"merge-file", "-p", conflictFlag, currentFile, baseFile, otherFile)
	if runErr != nil {
		if bytes.Contains(bytes.ToLower(cmdBytes.Stderr), []byte("binary")) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("git merge-file: %w: %s", runErr, string(cmdBytes.Stderr))
	}
	if isBinaryBlob(cmdBytes.Stdout) {
		return nil, true, nil
	}
	return cmdBytes.Stdout, false, nil
}

// readBlob reads raw object contents from git cat-file for the given blob SHA.
func readBlob(ctx context.Context, repoDir, sha string) ([]byte, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, maxBlobBytes, "cat-file", "blob", sha)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", shortSHA(sha), err)
	}
	return cmdBytes.Stdout, nil
}

// isBinaryBlob checks whether the initial byte slice contains a null byte.
func isBinaryBlob(b []byte) bool {
	limit := len(b)
	if limit > binaryProbeBytes {
		limit = binaryProbeBytes
	}
	return bytes.IndexByte(b[:limit], 0) != -1
}

// anyBinaryBlob reports whether any of the supplied byte slices is classified as binary.
func anyBinaryBlob(blobs ...[]byte) bool {
	for i := 0; i < len(blobs); i++ {
		if isBinaryBlob(blobs[i]) {
			return true
		}
	}
	return false
}

// treeBlobs returns a map of file path to blob SHA for the specified commit and paths.
func treeBlobs(ctx context.Context, repoDir, commit string, paths []string) (map[string]string, error) {
	blobs := make(map[string]string)
	if len(paths) == 0 || commit == "" {
		return blobs, nil
	}
	chunks, err := chunkPaths(paths, maxLsTreeArgBytes)
	if err != nil {
		return nil, err
	}
	for c := 0; c < len(chunks) && c < maxDiffFiles; c++ {
		if err := readTreeBlobChunk(ctx, repoDir, commit, chunks[c], blobs); err != nil {
			return nil, err
		}
	}
	return blobs, nil
}

// chunkPaths splits a list of file paths into chunks bounded by byte size.
func chunkPaths(paths []string, budget int) ([][]string, error) {
	if budget <= 0 {
		budget = maxLsTreeArgBytes
	}
	if len(paths) > maxDiffFiles {
		return nil, fmt.Errorf("mergecheck: changed file count %d exceeds limit %d", len(paths), maxDiffFiles)
	}
	var chunks [][]string
	var cur []string
	size := 0
	for i := 0; i < len(paths) && i < maxDiffFiles; i++ {
		need := len(paths[i]) + 1
		if size+need > budget && len(cur) > 0 {
			chunks = append(chunks, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, paths[i])
		size += need
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}

// readTreeBlobChunk populates the blobs map by running git ls-tree on a chunk of paths.
func readTreeBlobChunk(ctx context.Context, repoDir, commit string, chunk []string, blobs map[string]string) error {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	args := make([]string, 0, len(chunk)+7)
	args = append(args, "--literal-pathspecs", "ls-tree", "--full-tree", "-r", "-z", commit, "--")
	args = append(args, chunk...)
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, util.MaxCommandOutputBytes, args...)
	if err != nil {
		return err
	}
	entries, err := util.ParseGitTreeListing(cmdBytes.Stdout, false, maxDiffFiles)
	if err != nil {
		return fmt.Errorf("parse ls-tree for %s: %w", shortSHA(commit), err)
	}
	for i := 0; i < len(entries) && i < maxDiffFiles; i++ {
		if entries[i].Type == "blob" {
			blobs[entries[i].Path] = entries[i].Object
		}
	}
	return nil
}

// listMergeCommits retrieves merge commit hashes within commitRange bounded by maxMerges.
func listMergeCommits(ctx context.Context, repoDir, commitRange string, maxMerges int) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	out, err := util.RunGit(opCtx, repoDir, "rev-list", "--merges", "--max-count="+strconv.Itoa(maxMerges+1), commitRange)
	if err != nil {
		return nil, fmt.Errorf("mergecheck: read merge commits in %q: %w", commitRange, err)
	}
	lines := strings.Fields(out)
	if len(lines) > maxMerges {
		return nil, fmt.Errorf("mergecheck: commit range %s exceeds %d merge commits", commitRange, maxMerges)
	}
	return lines, nil
}

// commitParents returns the parent commit hashes of the specified commit.
func commitParents(ctx context.Context, repoDir, sha string) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	out, err := util.RunGit(opCtx, repoDir, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(out)
	if len(fields) <= 1 {
		return nil, nil
	}
	return fields[1:], nil
}

// mergeBase returns the best common ancestor commit between p1 and p2.
func mergeBase(ctx context.Context, repoDir, p1, p2 string) (string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	out, status, err := util.RunGitProbeStatus(opCtx, repoDir, maxMergeBaseBytes, "merge-base", p1, p2)
	if err != nil {
		return "", err
	}
	if status == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(out.Stdout)), nil
}

// filesChanged lists paths modified between base and p1.
func filesChanged(ctx context.Context, repoDir, base, p1 string) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, util.MaxCommandOutputBytes, "diff", "--name-only", "-z", base, p1)
	if err != nil {
		return nil, err
	}
	return parseNullSeparated(cmdBytes.Stdout, maxDiffFiles)
}

// parseNullSeparated parses null-delimited path strings up to maxItems.
func parseNullSeparated(raw []byte, maxItems int) ([]string, error) {
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return nil, nil
	}
	parts := bytes.Split(raw, []byte{0})
	items := make([]string, 0, min(len(parts), maxItems))
	limit := maxItems + 1
	for i := 0; i < len(parts) && i <= limit; i++ {
		str := string(parts[i])
		if str == "" {
			continue
		}
		if len(items) >= maxItems {
			return nil, fmt.Errorf("mergecheck: diff exceeds %d changed files", maxItems)
		}
		items = append(items, str)
	}
	return items, nil
}
