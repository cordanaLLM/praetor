package hiss

import (
	"context"
	"fmt"
	"maps"
	"os"
	slashpath "path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A ratchet rejection used to call every unbaselined finding "introduced", so upgrading praetor
// to a build with a new check turned an adopter's audit red and pointed at code nobody had
// touched since the baseline was recorded (#599). AttributeRatchet tells the two causes apart
// by asking the current checks about the code as the baseline's commit holds it.

const (
	// maxAttributedFiles bounds the files carrying new violations one attribution reads at the
	// baseline's commit (HISS-02); past it no attribution is made.
	maxAttributedFiles = 200
	// maxAttributedBlobs bounds the committed files one attribution copies, package siblings of
	// a Go call cycle included (HISS-02).
	maxAttributedBlobs = 1000
	// maxTreeListingBytes bounds the tree listing one attribution reads.
	maxTreeListingBytes = 8 << 20
	// maxRevParseBytes bounds the commit resolution output.
	maxRevParseBytes = 1024
	// baselineHistoryTimeout bounds the history walk that finds the commit which last changed the
	// baseline file; it grows with the history, so it gets more than util.GitProbeTimeout.
	baselineHistoryTimeout = 30 * time.Second
)

// AttributeRatchet explains why the baseline b, read from baselinePath, does not record each of
// r's new violations (#599). It scans the new violations' files as the baseline's commits hold
// them, with the current checks under opts, the policy the ratchet's own scan used, and hands the
// findings to RatchetResult.Attribute. current is that scan's infractions.
//
// The baseline's commits are b.CommitSHA, HEAD when `praetorctl baseline --record` ran, and the
// last commit on HEAD's history that changed baselinePath. The recorder scans the work tree, so
// a baseline recorded before the code it scanned was committed holds that code only at the
// second, and after a squash merge a fresh clone holds only the second. A finding either commit
// reports is not new.
//
// It never changes the verdict. A baseline that records no commit, commits this clone does not
// hold, or any read or scan that fails leaves every new violation unattributed and says why in
// r.AttributionNote, so Summary describes them as not in the baseline rather than as introduced;
// only a finding the baseline records at another line, which needs no commit to tell, is still
// tagged moved (RatchetResult.AttributeUntraced).
func AttributeRatchet(ctx context.Context, root, baselinePath string, opts ScanOptions, b *baseline.Baseline, current []baseline.Infraction, r *baseline.RatchetResult) {
	if r == nil || b == nil || len(r.NewViolations) == 0 {
		return
	}
	recorded := strings.TrimSpace(b.CommitSHA)
	if recorded == "" {
		r.AttributeUntraced(b, current, "the baseline records no commit to compare against")
		return
	}
	commits, err := baselineCommits(ctx, root, baselinePath, recorded)
	if err != nil {
		r.AttributeUntraced(b, current, err.Error())
		return
	}
	compared := make([]baseline.CommitFindings, 0, len(commits))
	for i := 0; i < len(commits); i++ {
		atCommit, err := findingsAtCommit(ctx, root, commits[i], r.NewViolations, opts)
		if err != nil {
			r.AttributeUntraced(b, current, err.Error())
			return
		}
		compared = append(compared, baseline.CommitFindings{Commit: commits[i], Findings: atCommit})
	}
	r.Attribute(b, current, compared)
}

// baselineCommits resolves the baseline's commits this clone holds: recorded, the commit the
// baseline names, and the last commit on HEAD's history that changed baselinePath, each once and
// as a full object name. A commit the clone lacks and a baseline file no commit holds are left
// out; with neither left, or a git read that is not answered, it fails.
func baselineCommits(ctx context.Context, root, baselinePath, recorded string) ([]string, error) {
	if err := util.ValidateExecArg(recorded); err != nil {
		return nil, fmt.Errorf("the baseline commit %q is not a commit name: %w", recorded, err)
	}
	named, err := resolveCommit(ctx, root, recorded)
	if err != nil {
		return nil, err
	}
	committed, err := lastBaselineCommit(ctx, root, baselinePath)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, commit := range []string{named, committed} {
		if commit != "" && !slices.Contains(commits, commit) {
			commits = append(commits, commit)
		}
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("commit %s is not in this clone, and no commit on HEAD's history changed the baseline file", recorded)
	}
	return commits, nil
}

// resolveCommit returns the full name of the commit name names in the clone at root, or "" when
// the clone holds no such commit. Any other answer from git is an error.
func resolveCommit(ctx context.Context, root, name string) (string, error) {
	out, status, err := util.RunGitProbeStatus(ctx, root, maxRevParseBytes, "rev-parse", "--verify", "--quiet", name+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve commit %s: %w", name, err)
	}
	if status != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(out.Stdout)), nil
}

// lastBaselineCommit returns the last commit on HEAD's history that changed baselinePath, or ""
// when none can be named: the file was never committed, it lies outside root, or the history
// walk ended at a shallow clone's boundary. The commit is one root's clone holds, as the history
// walk runs there.
func lastBaselineCommit(ctx context.Context, root, baselinePath string) (string, error) {
	rel, ok := pathBelow(root, baselinePath)
	if !ok {
		return "", nil
	}
	out, err := util.RunGitProbeWithin(ctx, root, maxRevParseBytes, baselineHistoryTimeout,
		"--literal-pathspecs", "log", "-1", "--format=%H %P", "HEAD", "--", "./"+rel)
	if err != nil {
		return "", fmt.Errorf("find the commit that last changed %s: %w", rel, err)
	}
	fields := strings.Fields(string(out.Stdout))
	if len(fields) != 1 {
		// No commit, or one with its parents: the commit that changed the file.
		return strings.Join(fields[:min(len(fields), 1)], ""), nil
	}
	// A commit without parents adds every file it holds. A shallow clone's boundary commit reads
	// that way whatever it changed, so there it names no commit that recorded the baseline.
	shallow, err := util.RunGitProbe(ctx, root, maxRevParseBytes, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", fmt.Errorf("tell whether the clone is shallow: %w", err)
	}
	if strings.TrimSpace(string(shallow.Stdout)) != "false" {
		return "", nil
	}
	return fields[0], nil
}

// pathBelow returns path relative to root, slash-separated, when it lies below root.
func pathBelow(root, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	absRoot, rootErr := filepath.Abs(root)
	absPath, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return "", false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// findingsAtCommit scans the files of violations as commit holds them and returns what the
// current checks report there.
func findingsAtCommit(ctx context.Context, root, commit string, violations []baseline.Infraction, opts ScanOptions) ([]baseline.Infraction, error) {
	files, packages := attributionScope(violations)
	if len(files)+len(packages) > maxAttributedFiles {
		return nil, fmt.Errorf("they span more than the %d files one attribution reads", maxAttributedFiles)
	}
	rep, err := scanAtCommit(ctx, root, commit, files, packages, opts)
	if err != nil {
		return nil, err
	}
	if rep.Incomplete() {
		return nil, fmt.Errorf("the scan at commit %s left part of its files unexamined (an analysis bound, or a file that does not parse)", commit)
	}
	return ConvertToBaseline(rep.Violations), nil
}

// attributionScope names what the scan at the baseline's commit must read: each violation's own
// file, and for a Go HISS-01 finding its whole directory, the package a call cycle spans
// (reportCallCycles). Paths are slash-separated and relative to the scanned root.
func attributionScope(violations []baseline.Infraction) (files, packages map[string]bool) {
	files, packages = make(map[string]bool), make(map[string]bool)
	for i := 0; i < len(violations); i++ {
		file := slashpath.Clean(baseline.NormalizePath(violations[i].FilePath))
		if violations[i].RuleID == "HISS-01" && strings.EqualFold(slashpath.Ext(file), ".go") {
			packages[slashpath.Dir(file)] = true
			continue
		}
		files[file] = true
	}
	return files, packages
}

// committedFile is one regular file of the baseline's commit the attribution copies.
type committedFile struct {
	object string
	path   string
}

// scanAtCommit copies files and the Go files of packages, as commit holds them, into a
// temporary root and scans it with opts. A file the commit does not hold is simply absent, so
// nothing is reported for it there.
//
// The copy is scanned as its own scope, never through git: when the temporary directory lies in
// a work tree that ignores it, git lists none of the copied files. Every copied file must then be
// read, or the scan fails: a file the scan skipped would report nothing at the commit and turn
// unchanged code into a new finding.
func scanAtCommit(ctx context.Context, root, commit string, files, packages map[string]bool, opts ScanOptions) (rep *ScanReport, err error) {
	entries, err := committedFiles(ctx, root, commit, files, packages)
	if err != nil {
		return nil, err
	}
	staged, err := os.MkdirTemp("", "praetor-hiss-commit-")
	if err != nil {
		return nil, fmt.Errorf("stage the files of commit %s: %w", commit, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(staged); rmErr != nil && err == nil {
			err = fmt.Errorf("remove the staged files of commit %s: %w", commit, rmErr)
		}
	}()
	if err := stageCommittedFiles(ctx, root, staged, entries); err != nil {
		return nil, err
	}
	rep, err = scanTree(ctx, staged, opts, everyPathVisible)
	if err != nil {
		return nil, err
	}
	if rep.Coverage.FilesRead != len(entries) {
		return nil, fmt.Errorf("the scan at commit %s read %d of the %d files copied from it", commit, rep.Coverage.FilesRead, len(entries))
	}
	return rep, nil
}

// committedFiles lists the regular, scannable files commit, a resolved commit, holds at the given
// paths. Paths are pathspecs relative to root, read literally.
func committedFiles(ctx context.Context, root, commit string, files, packages map[string]bool) ([]committedFile, error) {
	args := []string{"--literal-pathspecs", "ls-tree", "-z", "-l", commit, "--"}
	for _, file := range slices.Sorted(maps.Keys(files)) {
		args = append(args, "./"+file)
	}
	for _, dir := range slices.Sorted(maps.Keys(packages)) {
		args = append(args, directoryPathspec(dir))
	}
	out, err := util.RunGitProbe(ctx, root, maxTreeListingBytes, args...)
	if err != nil {
		return nil, fmt.Errorf("list the files of commit %s: %w", commit, err)
	}
	return selectCommittedFiles(out.Stdout, files, packages)
}

// directoryPathspec is the pathspec that lists the entries of dir, a slash-separated directory
// relative to the root that git reads pathspecs against; "." is the root itself.
func directoryPathspec(dir string) string {
	if dir == "." {
		return "./"
	}
	return "./" + dir + "/"
}

// selectCommittedFiles keeps the records of an `ls-tree -z -l` listing the scan reads: regular
// blobs within MaxScanFileSize whose path stays below the root, that are either a named file or
// a Go file of a named package. A record that does not parse fails the listing. More than
// maxAttributedBlobs of them is an error, never a cut list: a package copied in part can miss the
// call cycle the commit holds.
func selectCommittedFiles(listing []byte, files, packages map[string]bool) ([]committedFile, error) {
	records, err := util.ParseGitTreeListing(listing, true, maxTreeListingBytes/util.MinGitTreeRecordBytes)
	if err != nil {
		return nil, fmt.Errorf("read the tree listing: %w", err)
	}
	selected := make([]committedFile, 0, min(len(records), maxAttributedBlobs))
	for i := 0; i < len(records); i++ {
		if !scannableBlob(records[i]) || !inAttributionScope(records[i].Path, files, packages) {
			continue
		}
		if len(selected) == maxAttributedBlobs {
			return nil, fmt.Errorf("they need more than the %d committed files one attribution copies", maxAttributedBlobs)
		}
		selected = append(selected, committedFile{object: records[i].Object, path: records[i].Path})
	}
	return selected, nil
}

// inAttributionScope reports whether a committed path is a named file or a Go file of a named
// package.
func inAttributionScope(path string, files, packages map[string]bool) bool {
	ext := strings.ToLower(slashpath.Ext(path))
	return files[path] || (ext == ".go" && packages[slashpath.Dir(path)])
}

// scannableBlob accepts only a regular blob within MaxScanFileSize below the root that the scan
// may read: an extension a scanner owns, or a path a content scanner may claim from its bytes,
// such as an extensionless script or an Ansible playbook. Symlinks, submodules, trees and
// oversize files are refused, as the scan itself skips them.
func scannableBlob(entry util.GitTreeEntry) bool {
	return entry.RegularBlob() && entry.Size <= MaxScanFileSize &&
		filepath.IsLocal(filepath.FromSlash(entry.Path)) && mayScan(entry.Path)
}

// stageCommittedFiles writes each committed file below staged at its own relative path.
func stageCommittedFiles(ctx context.Context, root, staged string, entries []committedFile) error {
	for i := 0; i < len(entries) && i < maxAttributedBlobs; i++ {
		out, err := util.RunGitProbe(ctx, root, MaxScanFileSize+1, "cat-file", "blob", entries[i].object)
		if err != nil {
			return fmt.Errorf("read %s at the baseline commit: %w", entries[i].path, err)
		}
		rel := filepath.FromSlash(entries[i].path)
		if err := util.MkdirConfined(staged, filepath.Dir(rel), util.SecureDirPerm); err != nil {
			return fmt.Errorf("stage %s: %w", entries[i].path, err)
		}
		if err := util.WriteFileConfined(staged, rel, out.Stdout, util.SecureFilePerm); err != nil {
			return fmt.Errorf("stage %s: %w", entries[i].path, err)
		}
	}
	return nil
}
