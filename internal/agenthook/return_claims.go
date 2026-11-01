// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxCitationFileBytes     = 1 << 20
	maxReturnCitations       = 256
	maxClaimedRuns           = 64
	maxCollectedBashCommands = 65536
	maxClaimCommandWords     = 4096 // the shell reader bounds words too; this bounds the match loops (HISS-02)
	citationReadTimeout      = 5 * time.Second
	returnClaimDenyPrefix    = "[BLOCKED BY HISS] return claim: "
)

// evaluateReturnClaims judges return citations and claimed command runs for review and audit tasks.
func evaluateReturnClaims(ctx context.Context, client, root string, canonical Canonical, entry correlationEntry) Verdict {
	isReview, err := isReviewTask(ctx, root, entry.Task)
	if err != nil {
		return Verdict{Outcome: Skip, Reason: "return claims not judged: " + err.Error()}
	}
	if !isReview {
		return Verdict{Outcome: Allow}
	}
	citations, err := caveman.ExtractReturnCitations(canonical.Return)
	if err != nil {
		return Verdict{Outcome: Deny, Reason: returnClaimDenyPrefix + err.Error()}
	}
	misses, err := evaluateCitations(ctx, root, citations)
	if err != nil {
		return Verdict{Outcome: Skip, Reason: "return claims not judged: " + err.Error()}
	}
	claimedRuns, err := caveman.ExtractReturnRan(canonical.Return)
	if err != nil {
		misses = append(misses, "claimed runs: "+err.Error())
	}
	unmeasured, runMisses := checkClaimedRuns(ctx, client, canonical, claimedRuns)
	misses = append(misses, runMisses...)
	if len(misses) > 0 {
		return Verdict{Outcome: Deny, Reason: returnClaimDenyPrefix + strings.Join(misses, "; ")}
	}
	if unmeasured != "" {
		return Verdict{Outcome: Skip, Reason: "claimed runs unmeasured: " + unmeasured}
	}
	return Verdict{Outcome: Allow}
}

// evaluateCitations validates return citations beneath root within the parent context.
func evaluateCitations(ctx context.Context, root string, citations []caveman.ReturnCitation) ([]string, error) {
	var misses []string
	for i := 0; i < len(citations) && i < maxReturnCitations; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		miss := checkCitation(ctx, root, citations[i])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if miss != "" {
			misses = append(misses, miss)
		}
	}
	return misses, nil
}

// isReviewTask reports whether task matches a review or audit label in target_tasks.
func isReviewTask(ctx context.Context, root, task string) (bool, error) {
	if task == "" {
		return false, nil
	}
	_, labels, _, err := config.LoadRegisterRoutingAuthority(ctx, root)
	if err != nil {
		return false, fmt.Errorf("review labels unreadable: %w", err)
	}
	reviewLabels := filterReviewLabels(labels)
	return slices.Contains(reviewLabels, task), nil
}

// filterReviewLabels selects review and audit labels from the repository routing vocabulary.
func filterReviewLabels(labels []string) []string {
	var reviewLabels []string
	for i := 0; i < len(labels) && i < router.MaxTaskLabels; i++ {
		if isReviewLabelName(labels[i]) {
			reviewLabels = append(reviewLabels, labels[i])
		}
	}
	return reviewLabels
}

// isReviewLabelName picks review and audit labels out of the repository's routing vocabulary
// by name, so a label added to target_tasks is covered without a second list in code.
func isReviewLabelName(label string) bool {
	lower := strings.ToLower(label)
	return strings.Contains(lower, "review") || strings.Contains(lower, "audit")
}

// checkCitation verifies that a cited path exists within the repository root, is not an escaping
// symlink, and (if a line number is cited) that the line number does not exceed the file's line count.
func checkCitation(ctx context.Context, root string, c caveman.ReturnCitation) string {
	_, rel, outside, err := resolveCitationPath(root, c.Path)
	if err != nil {
		return fmt.Sprintf("%s: %v", c.Path, err)
	}
	if outside {
		return ""
	}
	readCtx, cancel := context.WithTimeout(ctx, citationReadTimeout)
	defer cancel()
	if err := readCtx.Err(); err != nil {
		return fmt.Sprintf("%s: read timeout: %v", c.Path, err)
	}
	dir, base, err := openCitationDir(readCtx, root, rel)
	if err != nil {
		return formatCitationOpenError(c.Path, err)
	}
	miss := inspectCitationTarget(readCtx, dir, base, c)
	if closeErr := dir.Close(); closeErr != nil && miss == "" {
		miss = fmt.Sprintf("%s: close: %v", c.Path, closeErr)
	}
	return miss
}

// openCitationDir opens the parent directory of rel beneath root as a confined directory handle.
func openCitationDir(ctx context.Context, root, rel string) (*os.Root, string, error) {
	dir, err := contextopt.OpenDirectoryIn(ctx, root, filepath.Dir(rel))
	if err != nil {
		return nil, "", fmt.Errorf("open directory in root: %w", err)
	}
	return dir, filepath.Base(rel), nil
}

// formatCitationOpenError maps directory opening and stat errors to user-facing failure strings.
func formatCitationOpenError(path string, err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("%s: file not found", path)
	}
	return fmt.Sprintf("%s: %v", path, err)
}

// inspectCitationTarget stats the target inside dir and validates directory vs file citations.
func inspectCitationTarget(ctx context.Context, dir *os.Root, base string, c caveman.ReturnCitation) string {
	st, err := dir.Stat(base)
	if err != nil {
		return formatCitationOpenError(c.Path, err)
	}
	if st.IsDir() {
		if c.Line > 0 {
			return fmt.Sprintf("%s:%d: directory citation cannot have a line number", c.Path, c.Line)
		}
		return ""
	}
	if c.Line <= 0 {
		return ""
	}
	return checkFileLineBound(ctx, dir, base, c)
}

// resolveCitationPath cleans and confines rel within root, reporting whether it lies outside root.
func resolveCitationPath(root, rel string) (string, string, bool, error) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) {
		r, ok := relativeToRoot(root, rel)
		if !ok {
			return "", "", true, nil
		}
		rel = r
	}
	target, err := util.ConfinePath(root, rel)
	if err != nil {
		return "", "", false, fmt.Errorf("confine citation path: %w", err)
	}
	return target, rel, false, nil
}

// relativeToRoot computes rel relative to clean root, returning false if rel escapes root.
func relativeToRoot(root, absPath string) (string, bool) {
	r, err := filepath.Rel(filepath.Clean(root), absPath)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return r, true
}

// checkFileLineBound reads the cited file through a context-aware confined snapshot and
// verifies that the cited line number does not exceed the line count.
func checkFileLineBound(ctx context.Context, dir *os.Root, base string, c caveman.ReturnCitation) string {
	data, err := contextopt.ReadRootSnapshot(ctx, dir, base)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fmt.Sprintf("%s:%d: read timeout: %v", c.Path, c.Line, err)
		}
		if errors.Is(err, util.ErrFileTooLarge) {
			return fmt.Sprintf("%s:%d: file exceeds 1 MiB bound", c.Path, c.Line)
		}
		return fmt.Sprintf("%s:%d: read error: %v", c.Path, c.Line, err)
	}
	lines := countFileLines(data)
	if c.Line > lines {
		return fmt.Sprintf("%s:%d: line %d exceeds file line count %d", c.Path, c.Line, c.Line, lines)
	}
	return ""
}

// countFileLines counts the number of newline-terminated or non-empty lines in data.
func countFileLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	trimmed := bytes.TrimSuffix(data, []byte{'\n'})
	if len(trimmed) == 0 {
		return 1
	}
	return bytes.Count(trimmed, []byte{'\n'}) + 1
}

// checkClaimedRuns compares claimed commands against Bash calls collected from the transcript.
func checkClaimedRuns(ctx context.Context, client string, canonical Canonical, claimedRuns []string) (string, []string) {
	if len(claimedRuns) == 0 {
		return "", nil
	}
	if client != "claude" || canonical.TranscriptPath == "" {
		return strings.Join(claimedRuns, ", "), nil
	}
	actuals, err := readClaudeBashCommands(ctx, canonical.TranscriptPath, canonical.AgentID)
	if err != nil {
		return strings.Join(claimedRuns, ", "), nil
	}
	if len(actuals) > maxCollectedBashCommands {
		return fmt.Sprintf("transcript exceeds %d Bash commands bound", maxCollectedBashCommands), nil
	}
	var misses []string
	for i := 0; i < len(claimedRuns) && i < maxClaimedRuns; i++ {
		cmd := claimedRuns[i]
		if !hasMatchingCommand(cmd, actuals) {
			misses = append(misses, fmt.Sprintf("%s: claimed run with no matching Bash tool call", cmd))
		}
	}
	return "", misses
}

// hasMatchingCommand reports whether claimed matches any executed command in actuals.
func hasMatchingCommand(claimed string, actuals []string) bool {
	claimed = strings.TrimSpace(claimed)
	if claimed == "" {
		return true
	}
	for i := 0; i < len(actuals) && i < maxCollectedBashCommands; i++ {
		if commandMatches(claimed, strings.TrimSpace(actuals[i])) {
			return true
		}
	}
	return false
}

// commandMatches reports whether claimed was executed within actual: the claimed words must lead
// one of actual's chained commands as forge.ShellFields and targetCommandSeparator split it, the
// reader workspace targeting uses, so an echoed or quoted string is never an executed command. A
// subshell or group opener on a command's first word, "(go" or "{go", is read through. An actual
// command the shell reader refuses matches only when it equals claimed.
func commandMatches(claimed, actual string) bool {
	if claimed == actual {
		return true
	}
	want, err := forge.ShellFields(claimed)
	if err != nil || len(want) == 0 {
		return false
	}
	fields, err := forge.ShellFields(actual)
	if err != nil {
		return false
	}
	start := 0
	for i := 0; i <= len(fields) && i <= maxClaimCommandWords; i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		if commandLeadsWith(fields[start:i], want) {
			return true
		}
		start = i + 1
	}
	return false
}

// commandLeadsWith reports whether the words of one executed command start with want, reading a
// leading subshell or group opener on the first word through.
func commandLeadsWith(command, want []string) bool {
	if len(command) < len(want) {
		return false
	}
	for i := 0; i < len(want) && i < maxClaimCommandWords; i++ {
		word := command[i]
		if i == 0 {
			word = strings.TrimLeft(word, "({")
		}
		if word != want[i] {
			return false
		}
	}
	return true
}
