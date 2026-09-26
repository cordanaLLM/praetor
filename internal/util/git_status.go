package util

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// gitProbeAutocrlf is the line-ending model every working-tree comparison runs under
// (RunGitTreeProbe). RunGitProbe drops the global configuration, which on Windows is where
// core.autocrlf=true usually lives, and git's live index can still carry stat entries an
// ordinary command refreshed under that setting, so without a pin a CRLF checkout of LF blobs
// compares as modified, or alternates between modified and clean, whenever git rehashes a
// file. The input mode normalises CRLF for the comparison only and never rewrites the working
// tree; git skips the conversion for a path whose indexed blob already holds CRLF, and
// attributes such as -text still override it.
const gitProbeAutocrlf = "core.autocrlf=input"

// maxGitFilterConfigBytes bounds the filter-configuration answer.
const maxGitFilterConfigBytes = 1 << 20

// GitTreeProbeTimeout is a bound for GitWorkingTreeChanges a caller without its own can pass.
// A status walk grows with the repository, untracked files and submodules included, so it is
// three times GitProbeTimeout; a refusal on timeout is still a refusal, never a clean result.
const GitTreeProbeTimeout = 3 * GitProbeTimeout

// maxReportedTreeChanges bounds how many changed paths DescribeWorkingTreeChanges names.
const maxReportedTreeChanges = 5

// ErrGitStatusFilters reports a repository whose own configuration names clean or process
// filters. git status runs them to compare a file with its indexed blob, so they would execute
// during a read-only probe and their output, not the file, would decide what status reports.
var ErrGitStatusFilters = errors.New("the repository configures git clean or process filters, which a status probe would execute")

// GitWorkingTreeChanges lists every path in dir's repository whose working-tree state differs
// from HEAD, as porcelain records: what git status reports -- staged, unstaged and untracked,
// down to each untracked file -- plus every index entry flagged assume-unchanged or
// skip-worktree, which git status never compares. It covers the whole repository even when
// dir is a subdirectory; pathspec narrows that further, relative to dir. The whole inspection
// runs within timeout, which the caller sizes to the repository; its own deadline still
// applies when earlier.
//
// Every repository setting that could hide a change is overridden: status.showUntrackedFiles
// by --untracked-files=all, submodule ignore settings by --ignore-submodules=none, core.fsmonitor
// and hooks by RunGitProbe. GIT_OPTIONAL_LOCKS=0 keeps the probe from rewriting the index. A
// repository that configures clean or process filters is refused with ErrGitStatusFilters
// rather than probed. An empty result means clean.
func GitWorkingTreeChanges(ctx context.Context, dir string, timeout time.Duration, pathspec ...string) ([]string, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("working-tree probe bound must be positive, got %v", timeout)
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := RefuseGitStatusFilters(probeCtx, dir); err != nil {
		return nil, err
	}
	scope := append([]string{"--", ":/"}, pathspec...)
	status, err := RunGitTreeProbe(probeCtx, dir, MaxCommandOutputBytes, timeout, append([]string{
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none"}, scope...)...)
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", dir, withCommandDiagnostic(err, status.Stderr))
	}
	index, err := RunGitProbeWithin(probeCtx, dir, MaxCommandOutputBytes, timeout, append([]string{"ls-files", "-v", "-z"}, scope...)...)
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, withCommandDiagnostic(err, index.Stderr))
	}
	return append(statusRecords(status.Stdout), hiddenIndexEntries(index.Stdout)...), nil
}

// RunGitTreeProbe runs a working-tree inspection through RunGitProbeWithin under the one
// line-ending model every comparison shares (gitProbeAutocrlf), so a status, a diff or an
// index listing reads a CRLF checkout the same way on every platform.
func RunGitTreeProbe(ctx context.Context, dir string, maxBytes int, timeout time.Duration, args ...string) (CommandBytes, error) {
	argv := make([]string, 0, len(args)+2)
	argv = append(argv, "-c", gitProbeAutocrlf)
	argv = append(argv, args...)
	return RunGitProbeWithin(ctx, dir, maxBytes, timeout, argv...)
}

// RefuseGitStatusFilters fails with ErrGitStatusFilters when dir's effective configuration --
// the repository's own, as RunGitProbe drops the global and system files -- names a clean or
// process filter, which a status or diff would execute. Any other failure to read the
// configuration is an error too, never an all-clear.
func RefuseGitStatusFilters(ctx context.Context, dir string) error {
	result, status, err := RunGitProbeStatus(ctx, dir, maxGitFilterConfigBytes,
		"config", "--name-only", "--get-regexp", `^filter\..*\.(clean|process)$`)
	if err != nil {
		return fmt.Errorf("inspect git filters in %s: %w", dir, withCommandDiagnostic(err, result.Stderr))
	}
	if status == 1 {
		return nil
	}
	names := strings.Fields(string(result.Stdout))
	return fmt.Errorf("%w: %s in %s", ErrGitStatusFilters, strings.Join(names, ", "), dir)
}

// GitLiteralExclude returns a pathspec that leaves rel out of a GitWorkingTreeChanges probe:
// rel is slash-separated, relative to the probed directory, and matched literally rather than
// as a glob.
func GitLiteralExclude(rel string) string {
	return ":(exclude,literal)" + rel
}

// DescribeWorkingTreeChanges summarises GitWorkingTreeChanges records as a count and the first
// few records, so a refusal names what differs without repeating an unbounded listing.
func DescribeWorkingTreeChanges(changes []string) string {
	shown := changes[:min(len(changes), maxReportedTreeChanges)]
	summary := fmt.Sprintf("%d changed path(s) differ from HEAD: %s", len(changes), strings.Join(shown, ", "))
	if len(changes) > len(shown) {
		summary += fmt.Sprintf(", and %d more", len(changes)-len(shown))
	}
	return summary
}

// GitHiddenIndexReason classifies one `git ls-files -v` tag: "skip-worktree" for S or s,
// "assume-unchanged" for any other lowercase letter, and "" for an entry git status compares.
func GitHiddenIndexReason(tag byte) string {
	switch {
	case tag == 'S' || tag == 's':
		return "skip-worktree"
	case tag >= 'a' && tag <= 'z':
		return "assume-unchanged"
	}
	return ""
}

// statusRecords returns one "XY path" record per change in `git status --porcelain=v1 -z`
// output. -z writes a rename's or copy's source path as a record of its own, directly after
// the change; it is folded into that change rather than counted as another.
func statusRecords(out []byte) []string {
	fields := splitNUL(out)
	records := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		record := fields[i]
		if isRenameOrCopy(record) && i+1 < len(fields) {
			i++
			record += " <- " + fields[i]
		}
		records = append(records, record)
	}
	return records
}

// isRenameOrCopy reports whether a porcelain record's status columns name a rename or copy.
func isRenameOrCopy(record string) bool {
	return len(record) > 2 && strings.ContainsAny(record[:2], "RC")
}

// hiddenIndexEntries returns the `git ls-files -v -z` records whose tag says git status does
// not compare them (GitHiddenIndexReason), each rendered as a porcelain-like record naming why
// it is reported.
func hiddenIndexEntries(out []byte) []string {
	fields := splitNUL(out)
	hidden := make([]string, 0)
	for i := 0; i < len(fields); i++ {
		tag, path, ok := strings.Cut(fields[i], " ")
		if !ok || len(tag) != 1 {
			continue
		}
		if reason := GitHiddenIndexReason(tag[0]); reason != "" {
			hidden = append(hidden, reason+" "+path)
		}
	}
	return hidden
}
