package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxBaselineListingBytes bounds one `git ls-tree` listing of a directory at the base
	// revision (HISS-02); a longer listing fails the run rather than being cut.
	maxBaselineListingBytes = 4 << 20
	// maxBaselineDiagnosticBytes bounds the git diagnostic an error quotes.
	maxBaselineDiagnosticBytes = 512
)

var errBaselineStdin = errors.New("caveman estimate: --base compares files with a git revision; standard input has none")

// baselineFile is one path of an `estimate --base` run: its working-tree spelling, the blob
// it was at the base revision ("" when it was no regular file there), whether the working
// tree holds it, and a directory of its repository that exists, for git to run in.
type baselineFile struct {
	path     string
	object   string
	worktree bool
	gitDir   string
}

// cavemanEstimateBase measures every path at a git revision and in the working tree and prints
// before, after and the signed difference per file and in total (#373). The revision is
// resolved once, in the repository of the first path, so every read sees one commit. A file on
// one side only is a row that says which side lacks it; a path on neither side is an error.
// A directory covers its Markdown files on both sides, so a file deleted since the base is
// reported, never dropped. Bytes are those of the blob as stored: on a checkout that converts
// line ends the working-tree figure includes the carriage returns, the token estimate does not.
func cavemanEstimateBase(ctx context.Context, rev string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(cavemanUsage)
	}
	commit, err := resolveBaselineCommit(ctx, rev, args[0])
	if err != nil {
		return err
	}
	files, err := baselineFiles(ctx, commit, args)
	if err != nil {
		return err
	}
	report := baselineReport{}
	for i := 0; i < len(files) && i <= maxCavemanFiles; i++ {
		delta, err := measureBaselineFile(ctx, files[i])
		if err != nil {
			return err
		}
		report.add(files[i], delta)
	}
	text := fmt.Sprintf("base: %s = %s\n%s%s base_absent=%d worktree_absent=%d\n", rev, commit, report.rows.String(),
		cavemanDeltaTotal(len(files), report.before, report.after), report.baseAbsent, report.worktreeAbsent)
	if _, err := io.WriteString(out, text); err != nil {
		return fmt.Errorf("caveman estimate: write report: %w", err)
	}
	return nil
}

// baselineReport collects the rows of one run, their sums and how many files one side lacks.
type baselineReport struct {
	rows                       strings.Builder
	before, after              cavemanMeasure
	baseAbsent, worktreeAbsent int
}

// add appends file's row. A file exists on at least one side, so at most one note applies.
func (r *baselineReport) add(file baselineFile, delta cavemanDelta) {
	r.before, r.after = r.before.plus(delta.before), r.after.plus(delta.after)
	note := ""
	switch {
	case file.object == "":
		note = " base=absent"
		r.baseAbsent++
	case !file.worktree:
		note = " worktree=absent"
		r.worktreeAbsent++
	}
	r.rows.WriteString(delta.row() + note + "\n")
}

// resolveBaselineCommit resolves rev to a commit in the repository that holds first, through
// the one ref resolver (resolveRefCommit). A revision git cannot be handed, or one that names
// no commit there, is an error naming it; so is a failed git read.
func resolveBaselineCommit(ctx context.Context, rev, first string) (string, error) {
	if first == "-" {
		return "", errBaselineStdin
	}
	dir := baselineGitDir(first)
	commit, ok, err := resolveRefCommit(ctx, dir, rev)
	if err != nil {
		return "", fmt.Errorf("caveman estimate: --base: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("caveman estimate: --base %q names no commit in the repository of %s", rev, filepath.ToSlash(dir))
	}
	return commit, nil
}

// baselineGitDir returns the directory git runs in for path: the path itself when it is a
// directory, its parent otherwise.
func baselineGitDir(path string) string {
	// #nosec G703 -- path is a file or directory the operator names on the command line to
	// measure, as with cat; no privilege boundary is crossed.
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return filepath.Dir(path)
}

// baselineFiles expands every argument into the files to compare, in argument order and
// lexical order below a directory, bounded by maxCavemanFiles.
func baselineFiles(ctx context.Context, commit string, args []string) ([]baselineFile, error) {
	var files []baselineFile
	for _, arg := range args {
		if arg == "-" {
			return nil, errBaselineStdin
		}
		expanded, err := baselineArgFiles(ctx, commit, arg)
		if err != nil {
			return nil, err
		}
		files = append(files, expanded...)
		if len(files) > maxCavemanFiles {
			return nil, fmt.Errorf("caveman: more than %d input files", maxCavemanFiles)
		}
	}
	return files, nil
}

func baselineArgFiles(ctx context.Context, commit, arg string) ([]baselineFile, error) {
	// #nosec G703 -- arg is a file or directory the operator names on the command line to
	// measure; the read itself goes through the bounded, symlink-resistant ReadSnapshot.
	info, err := os.Stat(arg)
	switch {
	case err == nil && info.IsDir():
		return baselineDirFiles(ctx, commit, arg)
	case err == nil:
		return baselineNamedFile(ctx, commit, arg, true)
	case errors.Is(err, fs.ErrNotExist):
		return baselineNamedFile(ctx, commit, arg, false)
	}
	return nil, fmt.Errorf("read %s: %w", arg, err)
}

// baselineNamedFile looks one named file up at the base revision. A path that is a file on
// neither side is an error: there is nothing to measure, and a typo must not read as a row.
func baselineNamedFile(ctx context.Context, commit, path string, worktree bool) ([]baselineFile, error) {
	dir := filepath.Dir(path)
	entries, err := baselineTree(ctx, commit, dir, false, "./"+filepath.Base(path))
	if err != nil {
		return nil, err
	}
	file := baselineFile{path: path, worktree: worktree, gitDir: dir}
	if len(entries) == 1 && entries[0].RegularBlob() {
		file.object = entries[0].Object
	}
	if !worktree && file.object == "" {
		return nil, fmt.Errorf("caveman estimate: %s is a file neither in the working tree nor at %s", filepath.ToSlash(path), commit)
	}
	return []baselineFile{file}, nil
}

// baselineDirFiles is the union of the Markdown files below dir in the working tree and at the
// base revision, so a file added since the base and one deleted since are both rows.
func baselineDirFiles(ctx context.Context, commit, dir string) ([]baselineFile, error) {
	markdown := map[string]bool{".md": true}
	present, err := appendCavemanFiles(ctx, nil, dir, markdown)
	if err != nil {
		return nil, err
	}
	entries, err := baselineTree(ctx, commit, dir, true, "")
	if err != nil {
		return nil, err
	}
	objects := make(map[string]string, len(entries))
	for _, entry := range entries {
		rel := filepath.FromSlash(entry.Path)
		if entry.RegularBlob() && filepath.IsLocal(rel) && markdown[strings.ToLower(filepath.Ext(rel))] {
			objects[filepath.Join(dir, rel)] = entry.Object
		}
	}
	files := make([]baselineFile, 0, len(present)+len(objects))
	for _, path := range present {
		files = append(files, baselineFile{path: path, object: objects[path], worktree: true, gitDir: dir})
		delete(objects, path)
	}
	for path, object := range objects {
		files = append(files, baselineFile{path: path, object: object, gitDir: dir})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("caveman estimate: directory %s holds no Markdown file in the working tree or at %s", filepath.ToSlash(dir), commit)
	}
	slices.SortFunc(files, func(a, b baselineFile) int { return strings.Compare(a.path, b.path) })
	return files, nil
}

// baselineTree lists commit's tree as seen from dir: `git ls-tree -z` run in dir shows only the
// entries below it, with paths relative to it, and nothing for a directory the commit does not
// hold; recursive descends into subdirectories, and pathspec, when set, names one entry of dir.
// It runs through util.RunGitProbe, the bounded read-only inspection (its own timeout, no
// hooks, no lazy fetch), with the pathspec read literally.
func baselineTree(ctx context.Context, commit, dir string, recursive bool, pathspec string) ([]util.GitTreeEntry, error) {
	argv := []string{"--literal-pathspecs", "ls-tree", "-z"}
	if recursive {
		argv = append(argv, "-r")
	}
	argv = append(argv, commit)
	if pathspec != "" {
		argv = append(argv, "--", pathspec)
	}
	result, err := util.RunGitProbe(ctx, dir, maxBaselineListingBytes, argv...)
	if err != nil {
		return nil, fmt.Errorf("caveman estimate: list %s at %s: %w", filepath.ToSlash(dir), commit, baselineGitError(err, result.Stderr))
	}
	entries, err := util.ParseGitTreeListing(result.Stdout, false, maxBaselineListingBytes/util.MinGitTreeRecordBytes)
	if err != nil {
		return nil, fmt.Errorf("caveman estimate: list %s at %s: %w", filepath.ToSlash(dir), commit, err)
	}
	return entries, nil
}

// baselineGitError appends git's bounded diagnostic to a failed inspection.
func baselineGitError(err error, stderr []byte) error {
	diagnostic := strings.TrimSpace(string(stderr))
	if diagnostic == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, util.TruncateExcerpt(diagnostic, maxBaselineDiagnosticBytes))
}

// measureBaselineFile measures one file on both sides; a side that lacks it measures zero.
func measureBaselineFile(ctx context.Context, file baselineFile) (cavemanDelta, error) {
	delta := cavemanDelta{name: filepath.ToSlash(file.path)}
	if file.object != "" {
		text, err := readBaselineBlob(ctx, file)
		if err != nil {
			return cavemanDelta{}, err
		}
		delta.before = measureCavemanText(text)
	}
	if file.worktree {
		input, err := readCavemanInput(ctx, file.path, nil)
		if err != nil {
			return cavemanDelta{}, err
		}
		delta.after = measureCavemanText(input.text)
	}
	return delta, nil
}

// readBaselineBlob reads the file's blob at the base revision under the bound and encoding the
// working-tree reader enforces: at most contextopt.MaxSourceBytes of UTF-8.
func readBaselineBlob(ctx context.Context, file baselineFile) (string, error) {
	name := filepath.ToSlash(file.path)
	result, err := util.RunGitProbe(ctx, file.gitDir, contextopt.MaxSourceBytes+1, "cat-file", "blob", file.object)
	if len(result.Stdout) > contextopt.MaxSourceBytes {
		return "", fmt.Errorf("caveman estimate: %s at the base revision exceeds %d bytes", name, contextopt.MaxSourceBytes)
	}
	if err != nil {
		return "", fmt.Errorf("caveman estimate: read %s at the base revision: %w", name, baselineGitError(err, result.Stderr))
	}
	if !utf8.Valid(result.Stdout) {
		return "", fmt.Errorf("caveman estimate: %s at the base revision is not UTF-8 text", name)
	}
	return string(result.Stdout), nil
}
