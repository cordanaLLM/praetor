package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// maxGitIgnoreQueryPaths bounds one GitIgnoredPaths query (HISS-02).
const maxGitIgnoreQueryPaths = 4096

// gitIgnoreOutputLimit bounds each stream of a check-ignore answer.
const gitIgnoreOutputLimit = 1 << 20

// GitIgnoredPaths returns the subset of relPaths, slash-separated and relative to the work
// tree at dir, that git ignores. The answer comes from the repository's own .gitignore files
// and .git/info/exclude: RunGitProbe isolates the global and system configuration, so an
// operator's personal excludes file never decides what the repository can commit.
//
// A path already in the index is not reported, because git tracks changes to it whatever its
// patterns say; noIndex asks about the patterns alone, for a probe path that is never tracked.
// Paths travel on standard input, so the query length never meets a command-line limit.
func GitIgnoredPaths(ctx context.Context, dir string, relPaths []string, noIndex bool) ([]string, error) {
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
	if noIndex {
		args = append(args, "--no-index")
	}
	result, status, err := RunGitProbeStatus(stdinCtx, dir, gitIgnoreOutputLimit, args...)
	if err != nil {
		return nil, fmt.Errorf("git check-ignore in %s: %w", dir, err)
	}
	if status == 1 {
		return nil, nil
	}
	return splitNUL(result.Stdout), nil
}

// checkIgnoreInput renders relPaths as the NUL-terminated records check-ignore --stdin -z
// reads, refusing a query past the bound and a path that is empty or would split a record.
func checkIgnoreInput(relPaths []string) ([]byte, error) {
	if len(relPaths) > maxGitIgnoreQueryPaths {
		return nil, fmt.Errorf("git check-ignore query exceeds %d paths", maxGitIgnoreQueryPaths)
	}
	var input bytes.Buffer
	for i := 0; i < len(relPaths) && i < maxGitIgnoreQueryPaths; i++ {
		if relPaths[i] == "" || strings.ContainsRune(relPaths[i], 0) {
			return nil, fmt.Errorf("git check-ignore path %q is empty or holds a NUL byte", relPaths[i])
		}
		input.WriteString(relPaths[i])
		input.WriteByte(0)
	}
	return input.Bytes(), nil
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
