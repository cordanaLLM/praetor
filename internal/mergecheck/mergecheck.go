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
)

// Finding records one file whose first-parent work was dropped in a merge commit.
type Finding struct {
	MergeSHA string
	FilePath string
	Parent1  string
	Parent2  string
	Deleted  bool
	Binary   bool
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
	m    map[string]string
	p1   map[string]string
	p2   map[string]string
	base map[string]string
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
	branch, err := forge.RepositoryDefaultBranch(ctx, repoDir, nil)
	if err != nil || branch == "" {
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

// FormatFinding formats one finding line naming merge sha12, file, and both parents.
// Order: engine-dropped-hunk-gate-20261027.
func FormatFinding(f Finding) string {
	mSha12 := shortSHA(f.MergeSHA)
	p1Sha12 := shortSHA(f.Parent1)
	p2Sha12 := shortSHA(f.Parent2)
	if f.Deleted {
		return fmt.Sprintf("[FAIL] merge %s: %s dropped first-parent changes (deleted in merge; parents %s, %s)",
			mSha12, f.FilePath, p1Sha12, p2Sha12)
	}
	if f.Binary {
		return fmt.Sprintf("[FAIL] merge %s: %s dropped first-parent changes (binary comparison; parents %s, %s)",
			mSha12, f.FilePath, p1Sha12, p2Sha12)
	}
	return fmt.Sprintf("[FAIL] merge %s: %s dropped first-parent changes (parents %s, %s)",
		mSha12, f.FilePath, p1Sha12, p2Sha12)
}

func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

func checkMergeCommit(ctx context.Context, repoDir, sha string) ([]Finding, []string, error) {
	parents, err := commitParents(ctx, repoDir, sha)
	if err != nil {
		return nil, nil, fmt.Errorf("read parents for %s: %w", shortSHA(sha), err)
	}
	if len(parents) < 2 {
		return nil, nil, nil
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
		finding, err := checkFileHunk(ctx, repoDir, mergeSHA, p1, p2, files[i], blobs)
		if err != nil {
			return nil, false, err
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
	}
	return findings, false, nil
}

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
	return &commitBlobs{m: bm, p1: bp1, p2: bp2, base: bBase}, nil
}

func checkFileHunk(ctx context.Context, repoDir, mergeSHA, p1, p2, file string, blobs *commitBlobs) (*Finding, error) {
	if blobs == nil {
		return nil, nil
	}
	blobM, okM := blobs.m[file]
	blobP1, okP1 := blobs.p1[file]
	blobP2, okP2 := blobs.p2[file]
	blobBase, okBase := blobs.base[file]

	if okP1 && !okM {
		return &Finding{
			MergeSHA: mergeSHA,
			FilePath: file,
			Parent1:  p1,
			Parent2:  p2,
			Deleted:  true,
		}, nil
	}
	if !okM || !okP2 || blobM != blobP2 || blobP2 == blobP1 {
		return nil, nil
	}
	return judgeDroppedHunk(ctx, repoDir, mergeSHA, p1, p2, file, blobP1, blobP2, blobBase, okBase)
}

func judgeDroppedHunk(ctx context.Context, repoDir, mergeSHA, p1, p2, file, blobP1, blobP2, blobBase string, okBase bool) (*Finding, error) {
	p1Bytes, err := readBlob(ctx, repoDir, blobP1)
	if err != nil {
		return nil, err
	}
	p2Bytes, err := readBlob(ctx, repoDir, blobP2)
	if err != nil {
		return nil, err
	}
	var baseBytes []byte
	if okBase {
		baseBytes, err = readBlob(ctx, repoDir, blobBase)
		if err != nil {
			return nil, err
		}
	}
	if isBinaryBlob(p1Bytes) || isBinaryBlob(p2Bytes) || isBinaryBlob(baseBytes) {
		return &Finding{MergeSHA: mergeSHA, FilePath: file, Parent1: p1, Parent2: p2, Binary: true}, nil
	}
	mergedBytes, isBin, err := reconstructMerge(ctx, repoDir, p1Bytes, baseBytes, p2Bytes)
	if err != nil {
		return nil, err
	}
	if isBin {
		return &Finding{MergeSHA: mergeSHA, FilePath: file, Parent1: p1, Parent2: p2, Binary: true}, nil
	}
	if bytes.Equal(mergedBytes, p2Bytes) {
		return nil, nil
	}
	return &Finding{MergeSHA: mergeSHA, FilePath: file, Parent1: p1, Parent2: p2, Binary: false}, nil
}

func reconstructMerge(ctx context.Context, repoDir string, p1Bytes, baseBytes, p2Bytes []byte) (merged []byte, isBin bool, err error) {
	tmpDir, err := os.MkdirTemp("", "mergecheck-blob-*")
	if err != nil {
		return nil, false, fmt.Errorf("create temp dir for merge-file: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tmpDir)) }()

	currentFile := filepath.Join(tmpDir, "current")
	baseFile := filepath.Join(tmpDir, "base")
	otherFile := filepath.Join(tmpDir, "other")

	if err := os.WriteFile(currentFile, p1Bytes, 0o600); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(baseFile, baseBytes, 0o600); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(otherFile, p2Bytes, 0o600); err != nil {
		return nil, false, err
	}

	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()

	cmdBytes, runErr := util.RunGitProbeWithin(opCtx, repoDir, maxBlobBytes, gitOpTimeout,
		"merge-file", "-p", "--theirs", currentFile, baseFile, otherFile)
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

func readBlob(ctx context.Context, repoDir, sha string) ([]byte, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, maxBlobBytes, "cat-file", "blob", sha)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", shortSHA(sha), err)
	}
	return cmdBytes.Stdout, nil
}

func isBinaryBlob(b []byte) bool {
	limit := len(b)
	if limit > binaryProbeBytes {
		limit = binaryProbeBytes
	}
	return bytes.IndexByte(b[:limit], 0) != -1
}

func treeBlobs(ctx context.Context, repoDir, commit string, paths []string) (map[string]string, error) {
	if len(paths) == 0 || commit == "" {
		return make(map[string]string), nil
	}
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	args := make([]string, 0, len(paths)+5)
	args = append(args, "ls-tree", "-r", "-z", commit, "--")
	for i := 0; i < len(paths) && i < maxDiffFiles; i++ {
		args = append(args, paths[i])
	}
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, util.MaxCommandOutputBytes, args...)
	if err != nil {
		return nil, err
	}
	return parseTreeBlobs(cmdBytes.Stdout, maxDiffFiles), nil
}

func parseTreeBlobs(raw []byte, maxEntries int) map[string]string {
	blobs := make(map[string]string)
	if len(raw) == 0 {
		return blobs
	}
	entries := bytes.Split(raw, []byte{0})
	for i := 0; i < len(entries) && i < maxEntries; i++ {
		entry := entries[i]
		if len(entry) == 0 {
			continue
		}
		tabIdx := bytes.IndexByte(entry, '\t')
		if tabIdx == -1 {
			continue
		}
		fields := strings.Fields(string(entry[:tabIdx]))
		if len(fields) >= 3 && fields[1] == "blob" {
			path := string(entry[tabIdx+1:])
			blobs[path] = fields[2]
		}
	}
	return blobs
}

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

func mergeBase(ctx context.Context, repoDir, p1, p2 string) (string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	out, status, err := util.RunGitProbeStatus(opCtx, repoDir, 64, "merge-base", p1, p2)
	if err != nil {
		return "", err
	}
	if status == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(out.Stdout)), nil
}

func filesChanged(ctx context.Context, repoDir, base, p1 string) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	cmdBytes, err := util.RunGitProbe(opCtx, repoDir, util.MaxCommandOutputBytes, "diff", "--name-only", "-z", base, p1)
	if err != nil {
		return nil, err
	}
	return parseNullSeparated(cmdBytes.Stdout, maxDiffFiles), nil
}

func parseNullSeparated(raw []byte, maxItems int) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{0})
	items := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && i < maxItems; i++ {
		str := string(parts[i])
		if str != "" {
			items = append(items, str)
		}
	}
	return items
}
