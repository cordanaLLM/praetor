package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// maxGitIgnoreQueryPaths bounds one GitIgnoredPaths query (HISS-02).
const maxGitIgnoreQueryPaths = 4096

// maxGitTrackedQueryPaths bounds one GitUntrackedPaths query, whose paths are arguments and so
// must stay well inside the shortest platform command-line limit (HISS-02, HISS-21).
const maxGitTrackedQueryPaths = 64

// gitIgnoreOutputLimit bounds each stream of a check-ignore or ls-files answer.
const gitIgnoreOutputLimit = 1 << 20

// gitIgnoreVerboseOutputLimit bounds a check-ignore -v answer, which adds the ignore file, line
// and pattern to every path.
const gitIgnoreVerboseOutputLimit = 4 << 20

// GitIgnoredPaths returns the subset of relPaths, slash-separated and relative to the work
// tree at dir, that git ignores. The answer comes from the repository's own .gitignore files
// and .git/info/exclude: RunGitProbe isolates the global and system configuration, so an
// operator's personal excludes file never decides what the repository can commit.
//
// A path already in the index is not reported, because git tracks changes to it whatever its
// patterns say; noIndex asks about the patterns alone, for a probe path that is never tracked.
// Paths travel on standard input, so the query length never meets a command-line limit.
func GitIgnoredPaths(ctx context.Context, dir string, relPaths []string, noIndex bool) ([]string, error) {
	out, err := runCheckIgnore(ctx, dir, relPaths, noIndex, false)
	if err != nil || out == nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// GitIgnoreMatch is one path git ignores and the rule that ignores it, as check-ignore -v
// reports it. When a parent directory of Path is ignored, the rule is the one that ignores
// that directory: git never looks inside it.
type GitIgnoreMatch struct {
	// Path is the queried path, as it was asked.
	Path string
	// Source is the file holding the rule, relative to the work tree (.gitignore, a nested
	// .gitignore, .git/info/exclude).
	Source string
	// Line is the rule's 1-based line in Source.
	Line int
	// Pattern is the rule as git parsed it; a directory-only rule keeps its trailing slash.
	Pattern string
}

// Rule names the match's rule for a report: "<source>:<line> (<pattern>)".
func (m GitIgnoreMatch) Rule() string {
	return fmt.Sprintf("%s:%d (%s)", m.Source, m.Line, m.Pattern)
}

// DirectoryOnly reports whether the rule matches directories alone (a trailing slash), so it
// never ignores a file of the same name.
func (m GitIgnoreMatch) DirectoryOnly() bool {
	return strings.HasSuffix(m.Pattern, "/")
}

// GitIgnoreMatches is GitIgnoredPaths with the rule behind each answer: the paths among
// relPaths that git ignores, in the order git reports them, each with the ignore file, line
// and pattern that decide it. The same isolation and index semantics apply. A path whose last
// matching rule is a negation is not ignored and is not returned. A path asked with a trailing
// slash is asked as a directory, whether or not it exists yet; ask directories here rather than
// through GitIgnoredPaths, which cannot drop the empty-pattern match (parseIgnoreMatches).
func GitIgnoreMatches(ctx context.Context, dir string, relPaths []string, noIndex bool) ([]GitIgnoreMatch, error) {
	out, err := runCheckIgnore(ctx, dir, relPaths, noIndex, true)
	if err != nil || out == nil {
		return nil, err
	}
	return parseIgnoreMatches(out)
}

// runCheckIgnore is the one check-ignore query behind GitIgnoredPaths and GitIgnoreMatches.
// It returns git's NUL-separated answer, or nil when git ignores none of relPaths.
func runCheckIgnore(ctx context.Context, dir string, relPaths []string, noIndex, verbose bool) ([]byte, error) {
	if len(relPaths) == 0 {
		return nil, nil
	}
	input, err := checkIgnoreInput(relPaths)
	if err != nil {
		return nil, err
	}
	stdinCtx, err := WithCommandStdin(ctx, input)
	if err != nil {
		return nil, err
	}
	args := []string{"check-ignore", "-z", "--stdin"}
	limit := gitIgnoreOutputLimit
	if verbose {
		args = append(args, "-v")
		limit = gitIgnoreVerboseOutputLimit
	}
	if noIndex {
		args = append(args, "--no-index")
	}
	result, status, err := RunGitProbeStatus(stdinCtx, dir, limit, args...)
	if err != nil {
		return nil, fmt.Errorf("git check-ignore in %s: %w", dir, err)
	}
	if status == 1 {
		return nil, nil
	}
	return result.Stdout, nil
}

// parseIgnoreMatches reads check-ignore -v -z records: source, line, pattern and path, each
// NUL-terminated. Verbose mode also reports a path whose last matching rule is a negation
// (the pattern starts with "!"); that path is not ignored and is dropped. So is a match on an
// empty pattern, which git makes of a blank line ending in a carriage return (a CRLF
// .gitignore): it matches only an empty last path component, which no file has and a
// directory probe with a trailing slash does, and a probed directory a real rule ignores is
// answered with that rule before git reaches the empty one.
func parseIgnoreMatches(out []byte) ([]GitIgnoreMatch, error) {
	fields := strings.Split(string(out), "\x00")
	if last := len(fields) - 1; fields[last] == "" {
		fields = fields[:last]
	}
	if len(fields)%4 != 0 {
		return nil, fmt.Errorf("git check-ignore -v answered %d fields, not whole records of 4", len(fields))
	}
	matches := make([]GitIgnoreMatch, 0, len(fields)/4)
	for i := 0; i+3 < len(fields) && i < 4*maxGitIgnoreQueryPaths; i += 4 {
		if fields[i+2] == "" || strings.HasPrefix(fields[i+2], "!") {
			continue
		}
		line, err := strconv.Atoi(fields[i+1])
		if err != nil {
			return nil, fmt.Errorf("git check-ignore -v answered line %q for %q: %w", fields[i+1], fields[i+3], err)
		}
		matches = append(matches, GitIgnoreMatch{Path: fields[i+3], Source: fields[i], Line: line, Pattern: fields[i+2]})
	}
	return matches, nil
}

// GitUntrackedPaths returns the subset of relPaths, slash-separated file paths relative to
// dir, that dir's index does not hold. It asks the index, not the ignore rules, so the answer
// does not depend on which excludes files a probe reads: a file any rule hides from git status
// is untracked here all the same, and a tracked file an ignore pattern matches is not. Each
// path is matched literally and must name a file; a directory prefix of tracked files is
// reported untracked. The paths travel as arguments, so one query is bounded more tightly
// than GitIgnoredPaths.
func GitUntrackedPaths(ctx context.Context, dir string, relPaths []string) ([]string, error) {
	if len(relPaths) == 0 {
		return nil, nil
	}
	if len(relPaths) > maxGitTrackedQueryPaths {
		return nil, fmt.Errorf("git ls-files query exceeds %d paths", maxGitTrackedQueryPaths)
	}
	args := make([]string, 0, len(relPaths)+3)
	args = append(args, "ls-files", "-z", "--")
	for i := 0; i < len(relPaths) && i < maxGitTrackedQueryPaths; i++ {
		if err := checkGitQueryPath("ls-files", relPaths[i]); err != nil {
			return nil, err
		}
		args = append(args, ":(literal)"+relPaths[i])
	}
	result, err := RunGitProbe(ctx, dir, gitIgnoreOutputLimit, args...)
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, withCommandDiagnostic(err, result.Stderr))
	}
	tracked := splitNUL(result.Stdout)
	untracked := make([]string, 0, len(relPaths))
	for i := 0; i < len(relPaths); i++ {
		if !slices.Contains(tracked, relPaths[i]) {
			untracked = append(untracked, relPaths[i])
		}
	}
	return untracked, nil
}

// checkIgnoreInput renders relPaths as the NUL-terminated records check-ignore --stdin -z
// reads, refusing a query past the bound and a path that is empty or would split a record.
func checkIgnoreInput(relPaths []string) ([]byte, error) {
	if len(relPaths) > maxGitIgnoreQueryPaths {
		return nil, fmt.Errorf("git check-ignore query exceeds %d paths", maxGitIgnoreQueryPaths)
	}
	var input bytes.Buffer
	for i := 0; i < len(relPaths) && i < maxGitIgnoreQueryPaths; i++ {
		if err := checkGitQueryPath("check-ignore", relPaths[i]); err != nil {
			return nil, err
		}
		input.WriteString(relPaths[i])
		input.WriteByte(0)
	}
	return input.Bytes(), nil
}

// checkGitQueryPath refuses a path query that is empty or holds a NUL byte, which would name
// the whole tree or split a record.
func checkGitQueryPath(command, rel string) error {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return fmt.Errorf("git %s path %q is empty or holds a NUL byte", command, rel)
	}
	return nil
}

// splitNUL splits NUL-terminated records, dropping the empty tail.
func splitNUL(out []byte) []string {
	fields := strings.Split(string(out), "\x00")
	paths := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		if fields[i] != "" {
			paths = append(paths, fields[i])
		}
	}
	return paths
}

// errGitProbeStatus reports a git exit status other than success or no-match.
var errGitProbeStatus = errors.New("git probe failed")
