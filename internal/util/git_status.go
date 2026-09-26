package util

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// GitProbeAutocrlf is the line-ending model a working-tree comparison runs under.
// RunGitProbe drops the global configuration, which on Windows is where core.autocrlf=true
// usually lives, so without a pin a CRLF checkout of LF blobs compares as modified whenever git
// rehashes a file. The input mode normalises CRLF for the comparison only and never rewrites
// the working tree; git skips the conversion for a path whose indexed blob already holds CRLF,
// and attributes such as -text still override it. internal/state pins the same model.
const GitProbeAutocrlf = "core.autocrlf=input"

// maxGitFilterConfigBytes bounds the filter-configuration answer.
const maxGitFilterConfigBytes = 1 << 20

// ErrGitStatusFilters reports a repository whose own configuration names clean or process
// filters. git status runs them to compare a file with its indexed blob, so they would execute
// during a read-only probe and their output, not the file, would decide what status reports.
var ErrGitStatusFilters = errors.New("the repository configures git clean or process filters, which a status probe would execute")

// GitWorkingTreeChanges lists every path in dir's repository whose working-tree state differs
// from HEAD, as porcelain records: what git status reports -- staged, unstaged and untracked,
// down to each untracked file -- plus every index entry flagged assume-unchanged or
// skip-worktree, which git status never compares. It covers the whole repository even when
// dir is a subdirectory; pathspec narrows that further, relative to dir.
//
// Every repository setting that could hide a change is overridden: status.showUntrackedFiles
// by --untracked-files=all, submodule ignore settings by --ignore-submodules=none, core.fsmonitor
// and hooks by RunGitProbe. GIT_OPTIONAL_LOCKS=0 keeps the probe from rewriting the index. A
// repository that configures clean or process filters is refused with ErrGitStatusFilters
// rather than probed. An empty result means clean.
func GitWorkingTreeChanges(ctx context.Context, dir string, pathspec ...string) ([]string, error) {
	if err := refuseGitStatusFilters(ctx, dir); err != nil {
		return nil, err
	}
	scope := append([]string{"--", ":/"}, pathspec...)
	status, err := RunGitProbe(ctx, dir, MaxCommandOutputBytes, append([]string{"-c", GitProbeAutocrlf,
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none"}, scope...)...)
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", dir, withCommandDiagnostic(err, status.Stderr))
	}
	index, err := RunGitProbe(ctx, dir, MaxCommandOutputBytes, append([]string{"ls-files", "-v", "-z"}, scope...)...)
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, withCommandDiagnostic(err, index.Stderr))
	}
	return append(statusRecords(status.Stdout), hiddenIndexEntries(index.Stdout)...), nil
}

// refuseGitStatusFilters fails when dir's effective configuration -- the repository's own, as
// RunGitProbe drops the global and system files -- names a clean or process filter.
func refuseGitStatusFilters(ctx context.Context, dir string) error {
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
// not compare them: a lowercase tag marks assume-unchanged, S skip-worktree. Each is rendered
// as a porcelain-like record naming why it is reported.
func hiddenIndexEntries(out []byte) []string {
	fields := splitNUL(out)
	hidden := make([]string, 0)
	for i := 0; i < len(fields); i++ {
		tag, path, ok := strings.Cut(fields[i], " ")
		if !ok || len(tag) != 1 {
			continue
		}
		switch {
		case tag == "S" || tag == "s":
			hidden = append(hidden, "skip-worktree "+path)
		case tag >= "a" && tag <= "z":
			hidden = append(hidden, "assume-unchanged "+path)
		}
	}
	return hidden
}
