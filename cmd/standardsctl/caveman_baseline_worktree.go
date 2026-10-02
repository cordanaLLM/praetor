package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxBaselineLocationBytes bounds git's answer to where a directory sits in its repository:
	// two paths.
	maxBaselineLocationBytes = 16384
	// maxBaselineSpellings bounds the tracked spellings one path is compared with (HISS-02). A
	// file system that ignores case holds one spelling per file; more than a few exist only
	// where case is distinguished, and there none of them is the named file.
	maxBaselineSpellings = 16
)

// requireBaselineDir refuses a directory argument that is a symlink, through the directory
// opener of the reader (contextopt.OpenDirectory), so the refusal is the one a file below
// such a link gets. The path to the directory may run through symlinks, as it may for the
// reader; the directory named may not be one.
func requireBaselineDir(ctx context.Context, dir string) error {
	root, err := contextopt.OpenDirectory(ctx, dir)
	if err != nil {
		return fmt.Errorf("caveman estimate: read %s: %w", filepath.ToSlash(dir), err)
	}
	if err := root.Close(); err != nil {
		return fmt.Errorf("caveman estimate: read %s: %w", filepath.ToSlash(dir), err)
	}
	return nil
}

// baselineWorktreeFiles returns the working-tree side of a directory: the Markdown files below
// dir that git tracks or would track, so both sides of the comparison describe one set. A file
// git ignores and one inside a nested repository are not part of the repository's text; counted,
// they are base=absent rows that inflate the total of a run at the repository root (ledgers,
// dependencies, other checkouts). A file the base holds stays a row even when git leaves it
// alone now, marked ignored: objects are the base files of dir by working-tree spelling.
func baselineWorktreeFiles(ctx context.Context, dir string, objects map[string]string) ([]baselineFile, error) {
	walked, err := appendCavemanFiles(ctx, nil, dir, cavemanProseExtensions())
	if err != nil {
		return nil, err
	}
	listing, err := baselineWorktreeListing(ctx, dir, "")
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(listing))
	for _, rel := range listing {
		listed[filepath.Join(dir, filepath.FromSlash(rel))] = true
	}
	files := make([]baselineFile, 0, len(walked))
	for _, path := range walked {
		object, atBase := objects[path]
		if listed[path] || atBase {
			files = append(files, baselineFile{path: path, object: object, worktree: true, ignored: !listed[path], gitDir: dir})
		}
	}
	return files, nil
}

// baselineWorktreeListing lists the files of the working tree that git tracks or would track,
// as seen from dir: the index plus the untracked files no ignore rule of the repository hides
// (`git ls-files --cached --others --exclude-standard`, the listing internal/docsref takes).
// A nested repository is one entry ending in "/" and a submodule one entry for its directory,
// so no file inside either is listed. pathspec, when set, names one path below dir and is read
// literally. The listing runs through util.RunGitProbe, whose environment reads no per-user
// configuration: the answer is the repository's, the same on every machine.
func baselineWorktreeListing(ctx context.Context, dir, pathspec string) ([]string, error) {
	argv := []string{"--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate"}
	if pathspec != "" {
		argv = append(argv, "--", pathspec)
	}
	result, err := util.RunGitProbe(ctx, dir, maxBaselineListingBytes, argv...)
	if err != nil {
		return nil, fmt.Errorf("caveman estimate: list the working tree of %s: %w", filepath.ToSlash(dir), baselineGitError(err, result.Stderr))
	}
	return splitGitNames(result.Stdout), nil
}

// splitGitNames splits a NUL-terminated git listing into its names.
func splitGitNames(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// baselineCaseVariant refuses a path the working tree holds while git tracks it, at commit or
// in the index, under a spelling that differs in letter case only. A file system that ignores
// case (the default on macOS and Windows) finds README.md under readme.md, git matches a path
// exactly, and the base side would silently read as absent. dir is the directory git runs in
// and rest the path below it, "" for dir itself.
//
// The tracked spellings come from one bounded listing taken at the top of the repository,
// where a pathspec is matched without regard to case over its whole length
// (`git ls-files --cached --with-tree=<commit> -- :(icase,literal)<path>`). A spelling counts
// only when it names the very file path names (os.SameFile), so where case is distinguished
// README.md and readme.md stay two files, and the second is a row without a base side.
func baselineCaseVariant(ctx context.Context, commit, path, dir, rest string) error {
	top, prefix, err := baselineLocation(ctx, dir)
	if err != nil {
		return err
	}
	named := strings.TrimSuffix(prefix+filepath.ToSlash(rest), "/")
	if named == "" {
		return nil
	}
	result, err := util.RunGitProbe(ctx, top, maxBaselineListingBytes,
		"ls-files", "-z", "--cached", "--with-tree="+commit, "--", ":(icase,literal)"+named)
	if err != nil {
		return fmt.Errorf("caveman estimate: list the spellings of %s: %w", filepath.ToSlash(path), baselineGitError(err, result.Stderr))
	}
	spellings := caseVariants(named, splitGitNames(result.Stdout))
	for i := 0; i < len(spellings) && i < maxBaselineSpellings; i++ {
		if sameWorktreeFile(path, filepath.Join(top, filepath.FromSlash(spellings[i]))) {
			return baselineSpellingError(path, spellings[i])
		}
	}
	return nil
}

// baselineSpellingError names the spelling git tracks a path under.
func baselineSpellingError(path, tracked string) error {
	return fmt.Errorf("caveman estimate: %s is tracked as %s; name it in that letter case, git finds no base side under another",
		filepath.ToSlash(path), tracked)
}

// baselineLocation returns the top of the work tree that holds dir and the path of dir below
// it, in git's slash form: "" at the top, otherwise ending in "/".
func baselineLocation(ctx context.Context, dir string) (string, string, error) {
	result, err := util.RunGitProbe(ctx, dir, maxBaselineLocationBytes, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return "", "", fmt.Errorf("caveman estimate: locate %s in its repository: %w", filepath.ToSlash(dir), baselineGitError(err, result.Stderr))
	}
	top, prefix, found := strings.Cut(strings.TrimSuffix(string(result.Stdout), "\n"), "\n")
	if !found || top == "" || strings.Contains(prefix, "\n") {
		return "", "", fmt.Errorf("caveman estimate: locate %s in its repository: git answered %q", filepath.ToSlash(dir), util.TruncateExcerpt(string(result.Stdout), maxBaselineDiagnosticBytes))
	}
	return filepath.FromSlash(top), prefix, nil
}

// caseVariants returns the spellings under which listing, a listing of files, holds named, a
// file or a directory, in another letter case: the leading segments of each entry, as many as
// named has, when they equal named but for case. Order is that of the listing, each once, at
// most maxBaselineSpellings of them.
func caseVariants(named string, listing []string) []string {
	depth := strings.Count(named, "/") + 1
	var variants []string
	for i := 0; i < len(listing) && len(variants) < maxBaselineSpellings; i++ {
		segments := strings.SplitN(listing[i], "/", depth+1)
		if len(segments) < depth {
			continue
		}
		spelling := strings.Join(segments[:depth], "/")
		if spelling != named && strings.EqualFold(spelling, named) && !slices.Contains(variants, spelling) {
			variants = append(variants, spelling)
		}
	}
	return variants
}

// sameWorktreeFile reports whether two spellings reach one file or directory. A spelling that
// cannot be read reaches nothing.
func sameWorktreeFile(path, other string) bool {
	// #nosec G703 -- both are spellings of a path the operator names on the command line, one
	// as typed and one as git tracks it; they are compared, never opened.
	first, err := os.Stat(path)
	if err != nil {
		return false
	}
	// #nosec G703 -- the tracked spelling of that same named path; compared, never opened.
	second, err := os.Stat(other)
	return err == nil && os.SameFile(first, second)
}
