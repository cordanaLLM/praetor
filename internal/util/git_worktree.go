package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxGitlinkBytes caps a .git gitlink file. Real gitlinks are one short line.
const MaxGitlinkBytes = 4096

// GitMetadataState classifies the state of .git metadata at a filesystem location.
//
// One decision (inspectGitEntry) produces every state and the walk decision, so the
// predicates built on it cannot disagree about the same entry. They do answer different
// questions, and the documented divergences are:
//
//   - A .git directory that holds a regular or symbolic HEAD but lacks objects/, refs/ or
//     a HEAD git accepts is Live for HasValidGitRepo (lightweight fixtures rely on it) but
//     does not stop the walk, because git skips it.
//   - A symlinked .git is followed by GitWorktreePresent and judged by its target, but
//     InspectGitMetadata does not follow it and reports Unknown, which deletion callers keep.
//
// Both are asserted by TestGitPredicates_Boundary_DocumentedDivergence. The mapping from
// state to the two predicates is:
//
//	state       meaning                                           walk stops  live
//	Live        directory with HEAD, or gitlink to such one       (1)         yes
//	Dangling    well-formed gitlink, target missing or not live   yes         no
//	Malformed   .git file without a "gitdir: <path>" line, empty  yes         no
//	            or oversized .git file, .git that is no file/dir
//	Absent      no entry, or a symlink to nothing (followed)      no          no
//	Headless    directory without HEAD                            no          no
//	Invalid     directory whose HEAD is not a file or symlink     no          no
//	Unknown     symlink not followed (InspectGitMetadata)         n/a         no
//
// (1) A live gitlink stops the walk. A live directory stops it only when git accepts it as
// a repository: objects/ and refs/ are directories and HEAD is a symlink into refs/, a
// "ref: refs/..." file or a file that starts with an object id (git's validate_headref).
//
// "walk stops" is what git does while it searches ancestors, probed against git 2.56 by
// TestGitWorktreePresent_Differential_AgreesWithGitRevParse: it skips a missing or dangling
// entry, a directory that is no repository and a directory with an unusable HEAD, but it
// stops at a .git file or special file that is no valid gitlink and dies ("invalid gitfile
// format", "no path in gitfile", "not a regular file"), and at a well-formed gitlink whose
// target is gone ("not a git repository"). GitWorktreePresent reports exactly the
// walk-stopping states, so the caller sees git's own error instead of an outer repository
// or a silent non-repository result. HasValidGitRepo reports Live only. Deletion callers
// treat Indeterminate states as protected.
type GitMetadataState uint8

const (
	GitMetadataUnknown GitMetadataState = iota
	GitMetadataAbsent
	GitMetadataHeadless
	GitMetadataLive
	GitMetadataDangling
	GitMetadataInvalid
	GitMetadataMalformed
)

// Indeterminate reports a state a caller must treat as possibly live: it is neither a
// confirmed absence nor a confirmed repository.
func (s GitMetadataState) Indeterminate() bool {
	return s == GitMetadataUnknown || s == GitMetadataDangling || s == GitMetadataInvalid || s == GitMetadataMalformed
}

// GitWorktreePresent reports whether git would find a repository at path or an ancestor,
// without converting a failed Git invocation into a non-repository result. Per ancestor
// it applies the rules of GitMetadataState: a live .git directory or gitlink is present,
// a well-formed gitlink is present even when its target is gone, a malformed .git file or
// special file is present too (git stops there and fails, and so must the caller), and
// every entry git skips (no entry, dangling symlink, directory without HEAD) continues
// the walk. A symlinked .git is followed. Real filesystem errors propagate.
//
// A .git directory git does not accept (no objects/ or refs/, or an unusable HEAD) is
// skipped here too, although HasValidGitRepo still calls a HEAD-only one Live.
func GitWorktreePresent(ctx context.Context, path string) (bool, error) {
	abs, err := validateWorktreeRoot(ctx, path)
	if err != nil {
		return false, err
	}
	for depth := 0; depth < 1024; depth++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		entry, err := inspectGitEntry(filepath.Join(abs, ".git"), true)
		if err != nil {
			return false, err
		}
		if entry.stopsWalk {
			return true, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false, nil
		}
		abs = parent
	}
	return false, errors.New("git worktree ancestry exceeds 1024 directories")
}

func validateWorktreeRoot(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", errors.New("git worktree detection requires a context")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git worktree root is not a directory: %s", path)
	}
	return abs, nil
}

// HasValidGitRepo reports whether path is a git working tree whose directory or gitlink
// metadata has a regular or legacy symbolic HEAD (state Live). An empty path is never a
// repository, including when the process working directory itself is a checkout.
func HasValidGitRepo(path string) bool {
	state, err := InspectWorktreeGitMetadata(path)
	return err == nil && state == GitMetadataLive
}

// InspectWorktreeGitMetadata inspects the .git metadata inside path. An empty or whitespace
// path is reported as absent metadata.
func InspectWorktreeGitMetadata(path string) (GitMetadataState, error) {
	if strings.TrimSpace(path) == "" {
		return GitMetadataAbsent, nil
	}
	return InspectGitMetadata(filepath.Join(path, ".git"))
}

// InspectGitMetadata inspects git metadata at gitPath (a .git directory or gitlink file)
// without following a symlink: a symlinked gitPath is Unknown, which deletion callers keep.
func InspectGitMetadata(gitPath string) (GitMetadataState, error) {
	entry, err := inspectGitEntry(gitPath, false)
	return entry.state, err
}

// gitEntry is the outcome of inspecting one .git entry: its state and whether git stops its
// ancestor search there.
type gitEntry struct {
	state     GitMetadataState
	stopsWalk bool
}

// inspectGitEntry is the single decision behind every predicate in this file. With follow
// set a symlinked gitPath is resolved and judged by its target; a dangling one is Absent
// (git skips it).
func inspectGitEntry(gitPath string, follow bool) (gitEntry, error) {
	info, err := os.Lstat(gitPath)
	if errors.Is(err, os.ErrNotExist) {
		return gitEntry{state: GitMetadataAbsent}, nil
	}
	if err != nil {
		return gitEntry{state: GitMetadataUnknown}, fmt.Errorf("lstat git metadata: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if !follow {
			return gitEntry{state: GitMetadataUnknown}, nil
		}
		info, err = os.Stat(gitPath)
		if errors.Is(err, os.ErrNotExist) {
			return gitEntry{state: GitMetadataAbsent}, nil
		}
		if err != nil {
			return gitEntry{state: GitMetadataUnknown}, fmt.Errorf("stat git metadata symlink: %w", err)
		}
	}
	switch {
	case info.IsDir():
		return inspectGitDirectoryEntry(gitPath)
	case info.Mode().IsRegular():
		state, err := inspectGitlinkFile(gitPath)
		return gitEntry{state: state, stopsWalk: gitlinkStopsWalk(state)}, err
	default:
		return gitEntry{state: GitMetadataMalformed, stopsWalk: true}, nil
	}
}

// gitlinkStopsWalk: git dies at a gitlink that is live, well formed with a missing target,
// or malformed; it never skips one.
func gitlinkStopsWalk(state GitMetadataState) bool {
	return state == GitMetadataLive || state == GitMetadataDangling || state == GitMetadataMalformed
}

// inspectGitDirectoryEntry classifies a .git directory and decides whether git accepts it
// as a repository (and so stops its ancestor search there).
func inspectGitDirectoryEntry(gitPath string) (gitEntry, error) {
	state, err := inspectGitDirectory(gitPath)
	if err != nil || state != GitMetadataLive {
		return gitEntry{state: state}, err
	}
	accepted, err := gitAcceptsDirectory(gitPath)
	return gitEntry{state: state, stopsWalk: accepted}, err
}

// gitAcceptsDirectory mirrors git's is_git_directory: objects/ and refs/ are directories
// and HEAD passes validateHeadRef. A missing entry rejects; any other failure propagates.
func gitAcceptsDirectory(gitPath string) (bool, error) {
	for _, name := range []string{"objects", "refs"} {
		sub, err := os.Stat(filepath.Join(gitPath, name))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("stat git %s: %w", name, err)
		}
		if !sub.IsDir() {
			return false, nil
		}
	}
	return validateHeadRef(filepath.Join(gitPath, "HEAD"))
}

// headRefProbeBytes bounds the HEAD prefix validateHeadRef reads: a "ref:" line needs a
// few bytes past the prefix and an object id needs at most 64 hex digits.
const headRefProbeBytes = 128

// validateHeadRef mirrors git's validate_headref: a symlink must point into refs/; a file
// must be "ref:" plus optional whitespace and a name starting with refs/, or begin with a
// hex object id of at least 40 digits. The refname format itself is not checked (git 2.56
// accepts "refs/heads/a..b"). Anything else, including a directory, is not a HEAD.
func validateHeadRef(headPath string) (bool, error) {
	info, err := os.Lstat(headPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lstat git HEAD: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(headPath)
		if err != nil {
			return false, fmt.Errorf("readlink git HEAD: %w", err)
		}
		return strings.HasPrefix(target, "refs/"), nil
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	head, err := readHeadPrefix(headPath)
	if err != nil {
		return false, err
	}
	return headContentValid(head), nil
}

func readHeadPrefix(headPath string) (data []byte, err error) {
	// #nosec G304 -- headPath is HEAD inside a .git directory the caller already inspected.
	file, err := os.Open(headPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open git HEAD: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			data, err = nil, fmt.Errorf("close git HEAD: %w", closeErr)
		}
	}()
	data, err = io.ReadAll(io.LimitReader(file, headRefProbeBytes))
	if err != nil {
		return nil, fmt.Errorf("read git HEAD: %w", err)
	}
	return data, nil
}

func headContentValid(head []byte) bool {
	text := string(head)
	if rest, ok := strings.CutPrefix(text, "ref:"); ok {
		return strings.HasPrefix(strings.TrimLeft(rest, " \t\r\n"), "refs/")
	}
	const minObjectIDHex = 40
	if len(text) < minObjectIDHex {
		return false
	}
	for _, c := range text[:minObjectIDHex] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// inspectGitDirectory inspects a git directory (such as a .git directory or a linked-worktree
// common directory) for a regular or symbolic HEAD file. A HEAD that is neither is Invalid.
// Whether git accepts the directory is decided separately by gitAcceptsDirectory.
func inspectGitDirectory(gitPath string) (GitMetadataState, error) {
	headInfo, err := os.Lstat(filepath.Join(gitPath, "HEAD"))
	if errors.Is(err, os.ErrNotExist) {
		return GitMetadataHeadless, nil
	}
	if err != nil {
		return GitMetadataUnknown, fmt.Errorf("lstat git HEAD: %w", err)
	}
	if headInfo.Mode().IsRegular() || headInfo.Mode()&os.ModeSymlink != 0 {
		return GitMetadataLive, nil
	}
	return GitMetadataInvalid, nil
}

func inspectGitlinkFile(gitPath string) (GitMetadataState, error) {
	worktree := filepath.Dir(gitPath)
	data, err := readGitlinkFile(gitPath)
	if err != nil {
		return GitMetadataUnknown, err
	}
	target, ok, err := resolveGitlinkData(worktree, data)
	if err != nil {
		return GitMetadataUnknown, err
	}
	if target == "" {
		return GitMetadataMalformed, nil
	}
	if !ok {
		return GitMetadataDangling, nil
	}
	state, err := inspectGitDirectory(target)
	if err != nil {
		return GitMetadataUnknown, err
	}
	if state != GitMetadataLive {
		return GitMetadataDangling, nil
	}
	return GitMetadataLive, nil
}

// readGitlinkFile reads a .git file, following a symlink at gitPath.
func readGitlinkFile(gitPath string) ([]byte, error) {
	return gitlinkReadResult(ReadFileLimited(gitPath, MaxGitlinkBytes))
}

// gitlinkReadResult is the one mapping for reading a gitlink: an oversized file is not a
// gitlink and reads as empty (parseGitlink rejects it); any other failure is wrapped.
func gitlinkReadResult(data []byte, err error) ([]byte, error) {
	switch {
	case errors.Is(err, ErrFileTooLarge):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read gitlink: %w", err)
	}
	return data, nil
}

// parseGitlink extracts the "gitdir: <path>" target from gitlink file contents. ok is false
// for anything else: a missing prefix, an empty path or empty contents.
func parseGitlink(data []byte) (string, bool) {
	line := strings.TrimRight(string(data), "\r\n")
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	target := line[len(prefix):]
	return target, target != ""
}

// ResolveGitlinkTarget reads the "gitdir: <path>" line of the .git file in worktree and
// returns the directory it names, cleaned and joined onto worktree when relative. ok is
// false when the file is no gitlink or its target is not an existing directory; err
// carries read and stat failures. An oversized file is not a valid gitlink and reports
// ok as false without an error.
func ResolveGitlinkTarget(worktree string) (string, bool, error) {
	data, err := gitlinkReadResult(ReadConfinedLimited(worktree, ".git", MaxGitlinkBytes))
	if err != nil {
		return "", false, err
	}
	return resolveGitlinkData(worktree, data)
}

// resolveGitlinkData resolves parsed gitlink contents; target is empty when data is no gitlink.
func resolveGitlinkData(worktree string, data []byte) (string, bool, error) {
	target, ok := parseGitlink(data)
	if !ok {
		return "", false, nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return target, false, nil
	}
	if err != nil {
		return target, false, fmt.Errorf("stat gitlink target: %w", err)
	}
	return target, info.IsDir(), nil
}
