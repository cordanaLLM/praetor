package util

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// statusFilterKeys matches the configuration keys that make git status run a program for a
// path: a driver's clean command and its long-running process. A smudge command only runs on
// checkout.
const statusFilterKeys = `^filter\..*\.(clean|process)$`

// gitFilterProbe runs one of the read-only inspections RefuseGitStatusFilters makes. RunGitProbe
// reads the repository's own configuration and attributes; effectiveGitProbe reads what a git
// command run under the caller's command environment reads.
type gitFilterProbe func(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error)

// effectiveGitProbe runs an inspection under the caller's command environment, so git reads the
// global and system configuration and attributes files that environment selects, with the
// filesystem monitor and hooks off and within GitProbeTimeout.
func effectiveGitProbe(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error) {
	return runGitInspection(ctx, dir, maxBytes, GitProbeTimeout, args...)
}

// configuredStatusFilters returns the clean and process keys dir's configuration defines, as
// probe reads it. The keys are sorted and unique, since a key set in more than one file is
// listed once per file.
func configuredStatusFilters(ctx context.Context, probe gitFilterProbe, dir string) ([]string, error) {
	answer, runErr := probe(ctx, dir, maxGitFilterConfigBytes,
		"config", "-z", "--name-only", "--get-regexp", statusFilterKeys)
	result, status, err := gitAnswerStatus(answer, runErr, maxGitFilterConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("inspect git filters in %s: %w", dir, withCommandDiagnostic(err, result.Stderr))
	}
	if status == 1 {
		return nil, nil
	}
	keys := splitNUL(result.Stdout)
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

// selectedFilterDrivers maps each value the filter attribute takes on a tracked path of dir's
// repository to the first such path, relative to dir. Git resolves the attribute itself, from
// the files probe lets it read -- .gitattributes files at every level, $GIT_DIR/info/attributes,
// macros and core.attributesFile, plus the global and system attributes files under
// effectiveGitProbe -- in the direction git status uses to clean a file. Untracked paths are
// left out: status lists them without cleaning them.
func selectedFilterDrivers(ctx context.Context, probe gitFilterProbe, dir string) (map[string]string, error) {
	tracked, err := probe(ctx, dir, MaxCommandOutputBytes, "ls-files", "-z", "--", ":/")
	if err != nil {
		return nil, fmt.Errorf("list tracked paths in %s: %w", dir, withCommandDiagnostic(err, tracked.Stderr))
	}
	if len(tracked.Stdout) == 0 {
		return nil, nil
	}
	stdinCtx, err := WithCommandStdin(ctx, tracked.Stdout)
	if err != nil {
		return nil, fmt.Errorf("read filter attributes in %s: %w", dir, err)
	}
	attributes, err := probe(stdinCtx, dir, MaxCommandOutputBytes, "check-attr", "--stdin", "-z", "filter")
	if err != nil {
		return nil, fmt.Errorf("read filter attributes in %s: %w", dir, withCommandDiagnostic(err, attributes.Stderr))
	}
	return filterAttributeValues(attributes.Stdout)
}

// filterAttributeValues parses `git check-attr -z filter` output -- path, attribute name and
// value, each NUL-terminated -- into the first path that carries each value. The states git
// prints for a bare, negated or absent attribute ("set", "unset", "unspecified") are kept as
// values too: a driver can carry one of those names, and a false match only refuses.
func filterAttributeValues(out []byte) (map[string]string, error) {
	fields := strings.Split(string(out), "\x00")
	if len(fields)%3 != 1 || fields[len(fields)-1] != "" {
		return nil, fmt.Errorf("git check-attr answer is not a sequence of NUL-terminated records")
	}
	values := make(map[string]string)
	for i := 0; i+2 < len(fields); i += 3 {
		if fields[i+1] != "filter" {
			return nil, fmt.Errorf("git check-attr answered attribute %q, not filter", fields[i+1])
		}
		if _, seen := values[fields[i+2]]; !seen {
			values[fields[i+2]] = fields[i]
		}
	}
	return values, nil
}

// filterDriverName returns the driver a filter.<driver>.<key> configuration key names. The
// driver is the subsection and may itself hold dots, so only the last one ends it.
func filterDriverName(key string) string {
	driver := strings.TrimPrefix(key, "filter.")
	if end := strings.LastIndexByte(driver, '.'); end >= 0 {
		return driver[:end]
	}
	return driver
}

// selectedStatusFilters returns, for each key of keys whose driver a tracked path selects, the
// key and that path, as "filter.<driver>.<key> (filter=<driver> on <path>)".
func selectedStatusFilters(keys []string, selected map[string]string) []string {
	refused := make([]string, 0, len(keys))
	for _, key := range keys {
		driver := filterDriverName(key)
		if path, ok := selected[driver]; ok {
			refused = append(refused, fmt.Sprintf("%s (filter=%s on %s)", key, driver, path))
		}
	}
	return refused
}
