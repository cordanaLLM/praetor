// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cifilter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const maxDiffLines = 200000

// LineRange records a 1-based inclusive range of lines added or modified in a file.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Overlaps reports whether r shares at least one line with the inclusive range [start, end].
func (r LineRange) Overlaps(start, end int) bool {
	return r.Start <= end && start <= r.End
}

// GetChangedLineRanges extracts the 1-based inclusive added or modified line ranges per file
// from git diff -U0 against the merge base of baseRef and headRef.
func GetChangedLineRanges(ctx context.Context, dir, baseRef, headRef string) (map[string][]LineRange, error) {
	if ctx == nil {
		return nil, errors.New("changed line ranges requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
	defer cancel()

	if dir == "" {
		dir = "."
	}
	if baseRef == "" {
		baseRef = "origin/main"
	}
	if headRef == "" {
		headRef = "HEAD"
	}

	out, err := runUnifiedZeroDiff(ctx, dir, baseRef, headRef)
	if err != nil {
		return nil, err
	}
	return parseUnifiedZeroDiff(out)
}

// runUnifiedZeroDiff runs git diff -U0 against the merge base of baseRef and headRef.
func runUnifiedZeroDiff(ctx context.Context, dir, baseRef, headRef string) (string, error) {
	flags := []string{"-c", "core.quotePath=false", "diff", "-U0", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/"}
	if headRef == "WORKTREE" {
		mb, err := resolveMergeBase(ctx, dir, baseRef, "HEAD")
		if err != nil {
			return "", err
		}
		out, err := util.RunGit(ctx, dir, append(flags, mb)...)
		if err != nil {
			return "", fmt.Errorf("git diff -U0 %s failed: %w", mb, err)
		}
		return out, nil
	}
	diffArg := fmt.Sprintf("%s...%s", baseRef, headRef)
	out, err := util.RunGit(ctx, dir, append(flags, diffArg)...)
	if err != nil {
		out, err = util.RunGit(ctx, dir, append(flags, baseRef, headRef)...)
		if err != nil {
			return "", fmt.Errorf("git diff -U0 %s %s failed: %w", baseRef, headRef, err)
		}
	}
	return out, nil
}

// resolveMergeBase returns the merge base of baseRef and headRef, falling back to a verified baseRef.
func resolveMergeBase(ctx context.Context, dir, baseRef, headRef string) (string, error) {
	mb, err := util.RunGit(ctx, dir, "merge-base", baseRef, headRef)
	if err == nil && strings.TrimSpace(mb) != "" {
		return strings.TrimSpace(mb), nil
	}
	verified, verifyErr := util.RunGit(ctx, dir, "rev-parse", "--verify", "--end-of-options", baseRef+"^{commit}")
	if verifyErr != nil {
		return "", fmt.Errorf("resolve base ref %s: %w", baseRef, verifyErr)
	}
	return strings.TrimSpace(verified), nil
}

// parseUnifiedZeroDiff parses git diff -U0 output into per-file added/modified line ranges.
func parseUnifiedZeroDiff(out string) (map[string][]LineRange, error) {
	result := make(map[string][]LineRange)
	if strings.TrimSpace(out) == "" {
		return result, nil
	}
	rawLines := strings.Split(out, "\n")
	if len(rawLines) > maxDiffLines {
		return nil, fmt.Errorf("git diff exceeds %d lines", maxDiffLines)
	}
	currentFile := ""
	for i := 0; i < len(rawLines); i++ {
		line := strings.TrimSuffix(rawLines[i], "\r")
		var err error
		currentFile, err = stepDiffLine(line, currentFile, result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// stepDiffLine processes one line of git diff -U0 output and updates the current file and ranges.
func stepDiffLine(line, currentFile string, result map[string][]LineRange) (string, error) {
	if strings.HasPrefix(line, "diff --git ") {
		return "", nil
	}
	if currentFile == "" && strings.HasPrefix(line, "+++ ") {
		return parseDiffTargetFile(line, result)
	}
	if currentFile != "" && strings.HasPrefix(line, "@@ ") {
		rng, ok, err := parseNewHunkRange(line)
		if err != nil {
			return "", err
		}
		if ok {
			result[currentFile] = append(result[currentFile], rng)
		}
	}
	return currentFile, nil
}

// parseDiffTargetFile extracts the b/ destination file path from a +++ header line.
func parseDiffTargetFile(line string, existing map[string][]LineRange) (string, error) {
	if line == "+++ /dev/null" {
		return "", nil
	}
	if !strings.HasPrefix(line, "+++ b/") {
		return "", fmt.Errorf("quoted or unsupported diff target header: %q", line)
	}
	path := strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
	if path == "" {
		return "", fmt.Errorf("empty diff target header: %q", line)
	}
	if _, ok := existing[path]; !ok && len(existing) >= maxFilesToInspect {
		return "", fmt.Errorf("git diff exceeds %d file entries", maxFilesToInspect)
	}
	if _, ok := existing[path]; !ok {
		existing[path] = nil
	}
	return path, nil
}

// parseNewHunkRange extracts the 1-based inclusive +start[,count] range from a unified diff hunk header.
func parseNewHunkRange(line string) (LineRange, bool, error) {
	endHeader := strings.Index(line[2:], " @@")
	if endHeader < 0 {
		return LineRange{}, false, fmt.Errorf("malformed diff hunk header: %q", line)
	}
	header := line[2 : 2+endHeader]
	plusIdx := strings.Index(header, " +")
	if plusIdx < 0 {
		return LineRange{}, false, fmt.Errorf("missing new range in diff hunk header: %q", line)
	}
	spec := header[plusIdx+2:]
	if spaceIdx := strings.IndexByte(spec, ' '); spaceIdx >= 0 {
		spec = spec[:spaceIdx]
	}
	startStr, countStr, hasComma := strings.Cut(spec, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 0 {
		return LineRange{}, false, fmt.Errorf("invalid hunk start in %q: %w", line, err)
	}
	count := 1
	if hasComma {
		count, err = strconv.Atoi(countStr)
		if err != nil || count < 0 {
			return LineRange{}, false, fmt.Errorf("invalid hunk count in %q: %w", line, err)
		}
	}
	if count == 0 || start == 0 {
		return LineRange{}, false, nil
	}
	return LineRange{Start: start, End: start + count - 1}, true, nil
}
