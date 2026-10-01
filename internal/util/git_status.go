package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// ErrGitStatusFilters reports a repository whose own configuration defines a clean or process
// filter that the attributes of a tracked path select. git status runs it to compare that file
// with its indexed blob, so it would execute during a read-only probe and its output, not the
// file, would decide what status reports.
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
// repository whose tracked paths select a clean or process filter it configures is refused
// with ErrGitStatusFilters (RefuseGitStatusFilters) rather than probed. An empty result means
// clean.
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
	statusArgs := append(globalExcludesArgs(probeCtx, dir), "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	status, err := RunGitTreeProbe(probeCtx, dir, MaxCommandOutputBytes, timeout, append(statusArgs, scope...)...)
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

// RefuseGitStatusFilters fails with ErrGitStatusFilters when a clean or process filter that
// dir's effective configuration defines -- the repository's own, as RunGitProbe drops the
// global and system files -- is selected by the filter attribute of a tracked path, so a status
// or diff would execute it. The refusal names each such key and the first path selecting it.
//
// A driver defined but selected by no tracked path passes: git runs a filter only for a path
// whose attributes name it. Git for Windows defines filter.lfs in its system gitconfig,
// `git lfs install` writes it into the global one and `git lfs install --local` into the
// repository's own, so testing the definition alone refused checkouts that use no filter at
// all (#640). Any failure to read the configuration or the attributes is an error, never an
// all-clear.
func RefuseGitStatusFilters(ctx context.Context, dir string) error {
	keys, err := configuredStatusFilters(ctx, dir)
	if err != nil || len(keys) == 0 {
		return err
	}
	selected, err := selectedFilterDrivers(ctx, dir)
	if err != nil {
		return err
	}
	refused := selectedStatusFilters(keys, selected)
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s in %s", ErrGitStatusFilters, strings.Join(refused, ", "), dir)
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

// globalExcludesArgs points the sealed status probe at the caller's global excludes file, so
// the probe treats as ignored exactly what a plain `git status` does. Git resolves
// core.excludesFile from the global config and otherwise falls back to
// $XDG_CONFIG_HOME/git/ignore or ~/.config/git/ignore; the probe's sealed environment
// (GIT_CONFIG_GLOBAL=/dev/null, no HOME) removes both, so a file only the user's global
// ignore hides (.DS_Store, .idea/) would read as an untracked change. The path is resolved
// here from the caller's environment and passed explicitly. This widens nothing: the
// repository-local .git/info/exclude is already honoured inside the probe.
func globalExcludesArgs(ctx context.Context, dir string) []string {
	path := configuredGlobalExcludes(ctx, dir)
	if path == "" {
		home := os.Getenv("HOME")
		if home == "" {
			// Git for Windows falls back to %USERPROFILE%, which os.UserHomeDir returns. No
			// home directory at all means no default excludes file, which is what git sees too.
			if userHome, err := os.UserHomeDir(); err == nil {
				home = userHome
			}
		}
		path = defaultGlobalExcludes(os.Getenv("XDG_CONFIG_HOME"), home)
	}
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return []string{"-c", "core.excludesFile=" + path}
}

// configuredGlobalExcludes reads core.excludesFile from the caller's global git config,
// expanded by git itself (--path). It returns "" when the key is unset or git fails.
func configuredGlobalExcludes(ctx context.Context, dir string) string {
	out, err := RunGit(ctx, dir, "config", "--global", "--path", "--get", "core.excludesFile")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// defaultGlobalExcludes is git's fallback location for the global excludes file.
func defaultGlobalExcludes(xdgConfigHome, home string) string {
	if xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "git", "ignore")
	}
	if home != "" {
		return filepath.Join(home, ".config", "git", "ignore")
	}
	return ""
}
