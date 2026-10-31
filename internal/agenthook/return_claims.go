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
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxCitationFileBytes  = 1 << 20
	maxReturnCitations    = 256
	maxClaimedRuns        = 64
	citationReadTimeout   = 5 * time.Second
	returnClaimDenyPrefix = "[BLOCKED BY HISS] return claim: "
)

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
	var misses []string
	for i := 0; i < len(citations) && i < maxReturnCitations; i++ {
		if err := ctx.Err(); err != nil {
			return Verdict{Outcome: Deny, Reason: returnClaimDenyPrefix + err.Error()}
		}
		if miss := checkCitation(ctx, root, citations[i]); miss != "" {
			misses = append(misses, miss)
		}
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

func checkCitation(ctx context.Context, root string, c caveman.ReturnCitation) string {
	target, rel, outside, err := resolveCitationPath(root, c.Path)
	if err != nil {
		return fmt.Sprintf("%s: %v", c.Path, err)
	}
	if outside {
		return ""
	}
	st, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("%s: file not found", c.Path)
		}
		return fmt.Sprintf("%s: %v", c.Path, err)
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
	return checkFileLineBound(ctx, root, rel, c)
}

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
		return "", "", false, err
	}
	return target, rel, false, nil
}

func relativeToRoot(root, absPath string) (string, bool) {
	r, err := filepath.Rel(filepath.Clean(root), absPath)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return r, true
}

func checkFileLineBound(ctx context.Context, root, rel string, c caveman.ReturnCitation) string {
	readCtx, cancel := context.WithTimeout(ctx, citationReadTimeout)
	defer cancel()
	if err := readCtx.Err(); err != nil {
		return fmt.Sprintf("%s:%d: read timeout: %v", c.Path, c.Line, err)
	}
	data, err := util.ReadConfinedLimited(root, rel, maxCitationFileBytes)
	if err != nil {
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

func checkClaimedRuns(ctx context.Context, client string, canonical Canonical, claimedRuns []string) (string, []string) {
	if len(claimedRuns) == 0 {
		return "", nil
	}
	if client != "claude" {
		return strings.Join(claimedRuns, ", "), nil
	}
	if canonical.TranscriptPath == "" {
		return strings.Join(claimedRuns, ", "), nil
	}
	actuals, err := readClaudeBashCommands(ctx, canonical.TranscriptPath, canonical.AgentID)
	if err != nil {
		return strings.Join(claimedRuns, ", "), nil
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

func hasMatchingCommand(claimed string, actuals []string) bool {
	claimed = strings.TrimSpace(claimed)
	if claimed == "" {
		return true
	}
	for i := 0; i < len(actuals) && i < 1024; i++ {
		if commandMatches(claimed, strings.TrimSpace(actuals[i])) {
			return true
		}
	}
	return false
}

func commandMatches(claimed, actual string) bool {
	if claimed == actual {
		return true
	}
	idx := strings.Index(actual, claimed)
	if idx < 0 {
		return false
	}
	if idx > 0 && !isCommandDelimiter(actual[idx-1]) {
		return false
	}
	end := idx + len(claimed)
	if end < len(actual) && !isCommandDelimiter(actual[end]) {
		return false
	}
	return true
}

func isCommandDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == ';' || b == '&' || b == '|' || b == '\n' || b == '(' || b == ')'
}
