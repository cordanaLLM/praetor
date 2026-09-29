package hiss

import (
	"context"
	"fmt"
	"maps"
	"os"
	slashpath "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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
	// treeRecordFields is the field count of an `ls-tree -l` record before its tab.
	treeRecordFields = 4
)

// AttributeRatchet explains why the baseline b does not record each of r's new violations
// (#599). It scans the new violations' files as they stood at b.CommitSHA, the commit the
// baseline was recorded at, with the current checks under opts, the policy the ratchet's own scan
// used, and hands the findings to RatchetResult.Attribute. current is that scan's infractions.
//
// It never changes the verdict. A baseline that records no commit, a commit this clone does not
// hold, or any read or scan that fails leaves every new violation unattributed and says why in
// r.AttributionNote, so Summary describes them as not in the baseline rather than as introduced;
// only a finding the baseline records at another line, which needs no commit to tell, is still
// tagged moved (RatchetResult.AttributeUntraced).
func AttributeRatchet(ctx context.Context, root string, opts ScanOptions, b *baseline.Baseline, current []baseline.Infraction, r *baseline.RatchetResult) {
	if r == nil || b == nil || len(r.NewViolations) == 0 {
		return
	}
	commit := strings.TrimSpace(b.CommitSHA)
	if commit == "" {
		r.AttributeUntraced(b, current, "the baseline records no commit to compare against")
		return
	}
	atCommit, err := findingsAtCommit(ctx, root, commit, r.NewViolations, opts)
	if err != nil {
		r.AttributeUntraced(b, current, err.Error())
		return
	}
	r.Attribute(b, current, atCommit, commit)
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

// committedFiles resolves commit and lists the regular, scannable files it holds at the given
// paths. Paths are pathspecs relative to root, read literally.
func committedFiles(ctx context.Context, root, commit string, files, packages map[string]bool) ([]committedFile, error) {
	if err := util.ValidateExecArg(commit); err != nil {
		return nil, fmt.Errorf("the baseline commit %q is not a commit name: %w", commit, err)
	}
	if _, err := util.RunGitProbe(ctx, root, maxRevParseBytes, "rev-parse", "--verify", "--quiet", commit+"^{commit}"); err != nil {
		return nil, fmt.Errorf("commit %s is not in this clone: %w", commit, err)
	}
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
	return selectCommittedFiles(string(out.Stdout), files, packages)
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
// a Go file of a named package. More than maxAttributedBlobs of them is an error, never a cut
// list: a package copied in part can miss the call cycle the commit holds.
func selectCommittedFiles(listing string, files, packages map[string]bool) ([]committedFile, error) {
	records := strings.Split(listing, "\x00")
	selected := make([]committedFile, 0, min(len(records), maxAttributedBlobs))
	for i := 0; i < len(records); i++ {
		entry, ok := parseTreeRecord(records[i])
		if !ok || !inAttributionScope(entry.path, files, packages) {
			continue
		}
		if len(selected) == maxAttributedBlobs {
			return nil, fmt.Errorf("they need more than the %d committed files one attribution copies", maxAttributedBlobs)
		}
		selected = append(selected, entry)
	}
	return selected, nil
}

// inAttributionScope reports whether a committed path is a named file or a Go file of a named
// package.
func inAttributionScope(path string, files, packages map[string]bool) bool {
	ext := strings.ToLower(slashpath.Ext(path))
	return files[path] || (ext == ".go" && packages[slashpath.Dir(path)])
}

// parseTreeRecord reads one `<mode> <type> <object> <size>\t<path>` record and accepts only a
// regular, scannable blob within MaxScanFileSize below the root; symlinks, submodules, trees and
// oversize files are refused, as the scan itself skips them.
func parseTreeRecord(record string) (committedFile, bool) {
	meta, path, found := strings.Cut(record, "\t")
	fields := strings.Fields(meta)
	if !found || len(fields) != treeRecordFields || fields[1] != "blob" {
		return committedFile{}, false
	}
	if fields[0] != "100644" && fields[0] != "100755" {
		return committedFile{}, false
	}
	size, err := strconv.Atoi(fields[3])
	if err != nil || size > MaxScanFileSize || !filepath.IsLocal(filepath.FromSlash(path)) || !SupportsExtension(slashpath.Ext(path)) {
		return committedFile{}, false
	}
	return committedFile{object: fields[2], path: path}, true
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
