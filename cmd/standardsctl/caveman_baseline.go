package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// tree holds it, whether git leaves that working-tree file alone (it neither tracks it nor
// would: an ignore rule or a nested repository covers it), and a directory of its repository
// that the working tree still holds, for git to run in.
type baselineFile struct {
	path     string
	object   string
	worktree bool
	ignored  bool
	gitDir   string
}

// cavemanEstimateBase measures every path at a git revision and in the working tree and prints
// before, after and the signed difference per file and in total (#373). The revision is
// resolved once, in the repository of the first path, so every read sees one commit. A file on
// one side only is a row that says which side lacks it; a path on neither side is an error.
// A directory covers its Markdown files on both sides, so a file deleted since the base is
// reported, never dropped; its working-tree side is the files git tracks or would track
// (baselineWorktreeFiles), the set the base side can hold. A file named outright is measured
// even when git ignores it, and its row says so. A path below a directory the working tree no
// longer holds is read the same way: git runs in the deepest directory that still exists
// (util.SplitAtExistingDir) and is handed the rest as a pathspec. Bytes are those of the blob
// as stored: on a checkout that converts line ends the working-tree figure includes the
// carriage returns, the token estimate does not.
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
	text := fmt.Sprintf("base: %s = %s\n%s%s base_absent=%d worktree_absent=%d worktree_ignored=%d\n", rev, commit, report.rows.String(),
		cavemanDeltaTotal(len(files), report.before, report.after), report.baseAbsent, report.worktreeAbsent, report.worktreeIgnored)
	if _, err := io.WriteString(out, text); err != nil {
		return fmt.Errorf("caveman estimate: write report: %w", err)
	}
	return nil
}

// baselineReport collects the rows of one run, their sums, how many files one side lacks and
// how many working-tree files git ignores.
type baselineReport struct {
	rows                                        strings.Builder
	before, after                               cavemanMeasure
	baseAbsent, worktreeAbsent, worktreeIgnored int
}

// add appends file's row. A file exists on at least one side, so base=absent and
// worktree=absent never meet; worktree=ignored marks a file that is there and can follow
// base=absent.
func (r *baselineReport) add(file baselineFile, delta cavemanDelta) {
	r.before, r.after = r.before.plus(delta.before), r.after.plus(delta.after)
	note := ""
	if file.object == "" {
		note = " base=absent"
		r.baseAbsent++
	}
	switch {
	case !file.worktree:
		note += " worktree=absent"
		r.worktreeAbsent++
	case file.ignored:
		note += " worktree=ignored"
		r.worktreeIgnored++
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
	split, err := splitBaselinePath(first)
	if err != nil {
		return "", err
	}
	commit, ok, err := resolveRefCommit(ctx, split.Dir, rev)
	if err != nil {
		return "", fmt.Errorf("caveman estimate: --base: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("caveman estimate: --base %q names no commit in the repository of %s", rev, filepath.ToSlash(split.Dir))
	}
	return commit, nil
}

// splitBaselinePath splits path at the deepest directory the working tree holds: git runs in
// split.Dir and split.Rest names the path from there. Starting git in a directory that was
// deleted or renamed since the base fails before git runs, with an error that reads as git not
// being installed.
func splitBaselinePath(path string) (util.DirSplit, error) {
	split, err := util.SplitAtExistingDir(path)
	if err != nil {
		return util.DirSplit{}, fmt.Errorf("caveman estimate: read %s: %w", filepath.ToSlash(path), err)
	}
	return split, nil
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
	split, err := splitBaselinePath(arg)
	if err != nil {
		return nil, err
	}
	if split.Rest == "" {
		return baselineDirFiles(ctx, commit, arg)
	}
	return baselineNamedPath(ctx, commit, arg, split)
}

// baselineNamedPath looks one named path up at the base revision, from the deepest directory
// the working tree holds. A file the working tree holds is one row, with or without a base
// side (baselineHeldFile). A path it lacks is whatever the base held there: a file is one row
// with no working-tree side, a directory is its Markdown files, each such a row. A path on
// neither side is an error: there is nothing to measure, and a typo must not read as a row.
func baselineNamedPath(ctx context.Context, commit, path string, split util.DirSplit) ([]baselineFile, error) {
	rest := filepath.ToSlash(split.Rest)
	// Without a working-tree side the base decides between file and directory, so the listing
	// descends; a single entry at rest itself is the file, entries below it are a directory.
	entries, err := baselineTree(ctx, commit, split.Dir, !split.Exists, "./"+rest)
	if err != nil {
		return nil, err
	}
	below := rest + "/"
	file := baselineFile{path: path, worktree: split.Exists, gitDir: split.Dir}
	tracked := ""
	if len(entries) == 1 && entries[0].RegularBlob() && !strings.HasPrefix(entries[0].Path, below) {
		file.object, tracked = entries[0].Object, entries[0].Path
	}
	if file.worktree {
		return baselineHeldFile(ctx, commit, file, rest, tracked)
	}
	if file.object != "" {
		return []baselineFile{file}, nil
	}
	files, err := baselineOnlyFiles(split.Dir, baselineMarkdownObjects(split.Dir, entries))
	switch {
	case err != nil || len(files) > 0:
		return files, err
	case slices.ContainsFunc(entries, func(entry util.GitTreeEntry) bool { return strings.HasPrefix(entry.Path, below) }):
		return nil, fmt.Errorf("caveman estimate: directory %s holds no Markdown file in the working tree or at %s", filepath.ToSlash(path), commit)
	}
	return nil, fmt.Errorf("caveman estimate: %s is a file neither in the working tree nor at %s", filepath.ToSlash(path), commit)
}

// baselineHeldFile completes the row of a named file the working tree holds; rest is its name
// in file.gitDir and tracked the spelling the base listing answered with, "" without a base
// side. Named under a letter case git does not track it under, on a file system that ignores
// case, the file is an error naming the tracked spelling: git matches a path exactly, so the
// base side would read as absent (baselineCaseVariant). A file git ignores is still measured,
// since the operator asked for it by name, and is marked.
func baselineHeldFile(ctx context.Context, commit string, file baselineFile, rest, tracked string) ([]baselineFile, error) {
	if tracked != rest {
		if err := baselineCaseVariant(ctx, commit, file.path, file.gitDir, rest); err != nil {
			return nil, err
		}
	}
	listed, err := baselineWorktreeListing(ctx, file.gitDir, "./"+rest)
	if err != nil {
		return nil, err
	}
	file.ignored = len(listed) == 0
	return []baselineFile{file}, nil
}

// baselineMarkdownObjects maps the Markdown files of a base listing taken in dir to their
// blobs, keyed by working-tree spelling. Only regular files with a local path count.
func baselineMarkdownObjects(dir string, entries []util.GitTreeEntry) map[string]string {
	objects := make(map[string]string, len(entries))
	for _, entry := range entries {
		rel := filepath.FromSlash(entry.Path)
		if entry.RegularBlob() && filepath.IsLocal(rel) && strings.EqualFold(filepath.Ext(rel), cavemanProseExtension) {
			objects[filepath.Join(dir, rel)] = entry.Object
		}
	}
	return objects
}

// baselineOnlyFiles turns objects, files of the base listing taken in gitDir that the walk of
// the working tree did not find, into rows in lexical order. Each is looked up once more by
// its tracked spelling (splitBaselinePath) before it is called absent: on a file system that
// ignores case the working tree can hold it under another letter case, and a path that now
// runs through a symlink is not a deleted file. Such a file is measured, or refused by the
// reader, never reported as a deletion. A lookup that fails is an error, not an absence.
func baselineOnlyFiles(gitDir string, objects map[string]string) ([]baselineFile, error) {
	files := make([]baselineFile, 0, len(objects))
	for path, object := range objects {
		split, err := splitBaselinePath(path)
		if err != nil {
			return nil, err
		}
		held := split.Exists && split.Rest != ""
		files = append(files, baselineFile{path: path, object: object, worktree: held, gitDir: gitDir})
	}
	sortBaselineFiles(files)
	return files, nil
}

func sortBaselineFiles(files []baselineFile) {
	slices.SortFunc(files, func(a, b baselineFile) int { return strings.Compare(a.path, b.path) })
}

// baselineDirFiles is the union of the Markdown files below dir in the working tree and at the
// base revision, so a file added since the base and one deleted since are both rows. The
// working-tree side is what git tracks or would track there (baselineWorktreeFiles). A
// symlink named as the directory is refused with the reader's own error: the walk does not
// follow it while git lists its target, which read every file of the target as deleted.
func baselineDirFiles(ctx context.Context, commit, dir string) ([]baselineFile, error) {
	if err := requireBaselineDir(ctx, dir); err != nil {
		return nil, err
	}
	entries, err := baselineTree(ctx, commit, dir, true, "")
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		if err := baselineCaseVariant(ctx, commit, dir, dir, ""); err != nil {
			return nil, err
		}
	}
	objects := baselineMarkdownObjects(dir, entries)
	files, err := baselineWorktreeFiles(ctx, dir, objects)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		delete(objects, file.path)
	}
	deleted, err := baselineOnlyFiles(dir, objects)
	if err != nil {
		return nil, err
	}
	files = append(files, deleted...)
	if len(files) == 0 {
		return nil, fmt.Errorf("caveman estimate: directory %s holds no Markdown file that git tracks or would track in the working tree, and none at %s", filepath.ToSlash(dir), commit)
	}
	sortBaselineFiles(files)
	return files, nil
}

// baselineTree lists commit's tree as seen from dir: `git ls-tree -z` run in dir shows only the
// entries below it, with paths relative to it, and nothing for a directory the commit does not
// hold; recursive descends into subdirectories, and pathspec, when set, names one path below
// dir, at any depth.
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
