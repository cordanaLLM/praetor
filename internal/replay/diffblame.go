// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// parsedHunk represents one unified-zero diff hunk between parent and commit.
type parsedHunk struct {
	oldFile  string
	newFile  string
	oldStart int
	oldCount int
	newStart int
	newCount int
}

// toFixHunk converts a parsed diff hunk into a FixHunk span.
func (h parsedHunk) toFixHunk() FixHunk {
	file := h.oldFile
	if file == "" {
		return FixHunk{}
	}
	start := max(h.oldStart, 1)
	return FixHunk{File: file, StartLine: start, EndLine: start + max(h.oldCount, 1) - 1}
}

// isAncestor reports whether ancestor is an ancestor of (or equal to) descendant.
func isAncestor(ctx context.Context, repoDir, ancestor, descendant string) (bool, error) {
	_, status, err := util.RunGitProbeStatus(ctx, repoDir, 1024, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, fmt.Errorf("check ancestor %s..%s: %w", ancestor, descendant, err)
	}
	return status == 0, nil
}

// revListBounded lists commits in spec bounded by MaxCommitScan.
func revListBounded(ctx context.Context, repoDir, spec string) ([]string, error) {
	limitArg := fmt.Sprintf("-n%d", MaxCommitScan+1)
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "rev-list", limitArg, spec, "--")
	if err != nil {
		return nil, fmt.Errorf("rev-list %s: %w", spec, err)
	}
	trimmed := strings.TrimSpace(string(res.Stdout))
	if trimmed == "" {
		return nil, nil
	}
	commits := strings.Split(trimmed, "\n")
	if len(commits) > MaxCommitScan {
		return nil, refusedf(ErrLimitExceeded, "range %s exceeds %d commits", spec, MaxCommitScan)
	}
	return commits, nil
}

// commitParents returns the parent OIDs of sha.
func commitParents(ctx context.Context, repoDir, sha string) ([]string, error) {
	parents, _, err := commitParentsAndSubject(ctx, repoDir, sha)
	return parents, err
}

// commitParentsAndSubject reads parent OIDs and commit subject line for sha.
func commitParentsAndSubject(ctx context.Context, repoDir, sha string) ([]string, string, error) {
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "show", "-s", "--format=%P%x00%s", sha, "--")
	if err != nil {
		return nil, "", fmt.Errorf("inspect commit %s: %w", sha, err)
	}
	raw := strings.TrimRight(string(res.Stdout), "\r\n")
	sep := strings.IndexByte(raw, 0)
	parentPart, subject := raw, ""
	if sep >= 0 {
		parentPart, subject = raw[:sep], raw[sep+1:]
	}
	var parents []string
	if strings.TrimSpace(parentPart) != "" {
		parents = strings.Fields(parentPart)
	}
	return parents, strings.TrimSpace(subject), nil
}

// candidateMessages collects the commit message of cand (and merged branch messages for merges).
func candidateMessages(ctx context.Context, repoDir, parent string, cand commitStamp) (string, error) {
	if len(cand.parents) >= 2 {
		if _, err := revListBounded(ctx, repoDir, parent+".."+cand.sha); err != nil {
			return "", err
		}
		limitArg := fmt.Sprintf("-n%d", MaxCommitScan+1)
		res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "log", "--format=%B", limitArg, parent+".."+cand.sha, "--")
		if err != nil {
			return "", fmt.Errorf("read merge messages %s: %w", cand.sha, err)
		}
		return string(res.Stdout), nil
	}
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "show", "-s", "--format=%B", cand.sha, "--")
	if err != nil {
		return "", fmt.Errorf("read commit message %s: %w", cand.sha, err)
	}
	return string(res.Stdout), nil
}

// readUnifiedZeroDiff runs git diff -U0 between two commits without external drivers or renames.
func readUnifiedZeroDiff(ctx context.Context, repoDir, fromSHA, toSHA string) ([]byte, error) {
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes,
		"diff", "--no-ext-diff", "--no-color", "--no-renames", "-U0", fromSHA, toSHA, "--")
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w", fromSHA, toSHA, err)
	}
	return res.Stdout, nil
}

// computePatchID computes git patch-id --stable over diffBytes, returning "" on empty diffs.
func computePatchID(ctx context.Context, repoDir string, diffBytes []byte) (string, error) {
	if len(diffBytes) == 0 {
		return "", nil
	}
	stdinCtx, err := util.WithCommandStdin(ctx, diffBytes)
	if err != nil {
		return "", fmt.Errorf("patch-id stdin: %w", err)
	}
	res, err := util.RunGitProbe(stdinCtx, repoDir, 4096, "patch-id", "--stable")
	if err != nil {
		return "", fmt.Errorf("git patch-id: %w", err)
	}
	fields := strings.Fields(string(res.Stdout))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// diffScanner accumulates parsed diff hunks and changed-line counts within MaxDiffLines.
type diffScanner struct {
	hunks        []parsedHunk
	oldFile      string
	newFile      string
	changedLines int
	oldRemaining int
	newRemaining int
}

// parseDiffHunksBounded parses unified-zero hunks and enforces MaxDiffLines on +/- lines.
func parseDiffHunksBounded(diffBytes []byte) ([]parsedHunk, error) {
	if len(diffBytes) == 0 {
		return nil, nil
	}
	lines := strings.Split(string(diffBytes), "\n")
	if len(lines) > maxScanLines {
		return nil, refusedf(ErrLimitExceeded, "diff exceeds %d raw lines", maxScanLines)
	}
	var scan diffScanner
	for i := 0; i < len(lines) && i < maxScanLines; i++ {
		if err := scan.consumeLine(lines[i]); err != nil {
			return nil, err
		}
	}
	return scan.hunks, nil
}

// consumeLine processes one unified diff line and checks MaxDiffLines.
func (s *diffScanner) consumeLine(line string) error {
	if s.oldRemaining > 0 || s.newRemaining > 0 {
		return s.consumeContent(line)
	}
	switch {
	case strings.HasPrefix(line, "--- "):
		s.oldFile = cleanDiffPath(strings.TrimPrefix(line, "--- "))
	case strings.HasPrefix(line, "+++ "):
		s.newFile = cleanDiffPath(strings.TrimPrefix(line, "+++ "))
	case strings.HasPrefix(line, "@@ "):
		hunk, err := parseHunkHeader(line, s.oldFile, s.newFile)
		if err != nil {
			return err
		}
		s.hunks = append(s.hunks, hunk)
		s.oldRemaining, s.newRemaining = hunk.oldCount, hunk.newCount
	case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
		return fmt.Errorf("diff content outside declared hunk")
	}
	return nil
}

// consumeContent consumes declared hunk lines before recognizing file headers again.
func (s *diffScanner) consumeContent(line string) error {
	if line == "" || strings.HasPrefix(line, "\\ No newline") {
		return nil
	}
	switch line[0] {
	case '-':
		s.oldRemaining--
		s.changedLines++
	case '+':
		s.newRemaining--
		s.changedLines++
	case ' ':
		s.oldRemaining--
		s.newRemaining--
	default:
		return fmt.Errorf("invalid diff hunk content %q", line)
	}
	if s.oldRemaining < 0 || s.newRemaining < 0 {
		return fmt.Errorf("diff hunk exceeds declared span")
	}
	if s.changedLines > MaxDiffLines {
		return refusedf(ErrLimitExceeded, "diff exceeds %d changed lines", MaxDiffLines)
	}
	return nil
}

// parseHunkHeader parses "@@ -oldStart[,oldCount] +newStart[,newCount] @@".
func parseHunkHeader(line, oldFile, newFile string) (parsedHunk, error) {
	rest, ok := strings.CutPrefix(line, "@@ ")
	if !ok {
		return parsedHunk{}, fmt.Errorf("invalid hunk header %q", line)
	}
	endIdx := strings.Index(rest, " @@")
	if endIdx < 0 {
		return parsedHunk{}, fmt.Errorf("unterminated hunk header %q", line)
	}
	header := rest[:endIdx]
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return parsedHunk{}, fmt.Errorf("malformed hunk ranges %q", line)
	}
	oldStart, oldCount, err := parseSpan(fields[0][1:])
	if err != nil {
		return parsedHunk{}, err
	}
	newStart, newCount, err := parseSpan(fields[1][1:])
	if err != nil {
		return parsedHunk{}, err
	}
	return parsedHunk{oldFile: oldFile, newFile: newFile, oldStart: oldStart, oldCount: oldCount, newStart: newStart, newCount: newCount}, nil
}

// parseSpan parses "start" (count 1) or "start,count".
func parseSpan(raw string) (int, int, error) {
	startStr, countStr, hasComma := strings.Cut(raw, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hunk line %q: %w", raw, err)
	}
	if !hasComma {
		return start, 1, nil
	}
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hunk count %q: %w", raw, err)
	}
	return start, count, nil
}

// blamePRHunks runs git blame at parentSHA on removed/modified hunk ranges and returns PR-attributed hunks.
func blamePRHunks(ctx context.Context, repoDir, parentSHA string, hunks []parsedHunk, prCommits map[string]bool) ([]FixHunk, error) {
	var matched []FixHunk
	for i := 0; i < len(hunks) && i < MaxDiffLines; i++ {
		h := hunks[i]
		if h.oldCount <= 0 || h.oldFile == "" {
			continue
		}
		endLine := h.oldStart + h.oldCount - 1
		rangeArg := fmt.Sprintf("%d,%d", h.oldStart, endLine)
		res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes,
			"blame", "--porcelain", "-L", rangeArg, parentSHA, "--", h.oldFile)
		if err != nil {
			return nil, fmt.Errorf("blame %s:%s at %s: %w", h.oldFile, rangeArg, parentSHA, err)
		}
		_, _, hit := extractPRBlameSpan(string(res.Stdout), prCommits)
		if hit {
			matched = append(matched, h.toFixHunk())
		}
	}
	return matched, nil
}

// extractPRBlameSpan scans git blame --porcelain output for commit OIDs belonging to prCommits.
func extractPRBlameSpan(porcelain string, prCommits map[string]bool) (int, int, bool) {
	lines := strings.Split(porcelain, "\n")
	hit := false
	minLine, maxLine := 0, 0
	for i := 0; i < len(lines) && i < maxScanLines; i++ {
		fields := strings.Fields(lines[i])
		if len(fields) < 3 || !util.ValidGitObjectID(fields[0]) || !prCommits[fields[0]] {
			continue
		}
		origLine, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		if !hit || origLine < minLine {
			minLine = origLine
		}
		if !hit || origLine > maxLine {
			maxLine = origLine
		}
		hit = true
	}
	return minLine, maxLine, hit
}

// cleanDiffPath normalizes git diff header file paths, returning "" for /dev/null.
func cleanDiffPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "a/") || strings.HasPrefix(trimmed, "b/") {
		trimmed = trimmed[2:]
	}
	return strings.TrimPrefix(trimmed, "./")
}

// appendUniqueHunks appends non-duplicate FixHunk entries bounded by MaxDiffLines.
func appendUniqueHunks(dst, add []FixHunk) ([]FixHunk, error) {
	if len(dst) > MaxDiffLines || len(add) > MaxDiffLines {
		return nil, refusedf(ErrLimitExceeded, "too many fix hunks")
	}
	for i := 0; i < len(add) && i < MaxDiffLines; i++ {
		if slices.Contains(dst, add[i]) {
			continue
		}
		if len(dst) == MaxDiffLines {
			return nil, refusedf(ErrLimitExceeded, "aggregate fix hunks exceed %d", MaxDiffLines)
		}
		dst = append(dst, add[i])
	}
	return dst, nil
}

// isHexByte reports whether b is an ASCII hexadecimal character.
func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// isDigit reports whether b is an ASCII decimal digit.
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// isAlphaNum reports whether b is an ASCII letter or digit.
func isAlphaNum(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
