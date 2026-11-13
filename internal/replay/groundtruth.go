// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
)

// prWindow holds the resolved commits, subjects, and patch IDs of a replayed pull request.
type prWindow struct {
	number    int
	baseSHA   string
	mergeSHA  string
	commits   []string
	set       map[string]bool
	subjects  []string
	patchIDs  map[string]bool
	cutoffSHA string
}

// GroundTruth derives post-merge repair commits and fix hunks on base up to until.
func GroundTruth(ctx context.Context, repoDir string, pr PRSpec, cutoff, until time.Time) (*GroundTruthResult, error) {
	if err := validateGroundTruthInputs(ctx, repoDir, pr, cutoff, until); err != nil {
		return nil, err
	}
	baseName, baseTip, err := resolveBaseTip(ctx, repoDir, pr.Base)
	if err != nil {
		return nil, err
	}
	win, err := resolvePRWindow(ctx, repoDir, baseName, baseTip, pr)
	if err != nil {
		return nil, err
	}
	if err := resolveHistoricalWindow(ctx, repoDir, baseName, win, cutoff); err != nil {
		return nil, err
	}
	candidates, err := listLaterCommits(ctx, repoDir, win.mergeSHA, baseTip, cutoff.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	return collectGroundTruth(ctx, repoDir, win, candidates)
}

// collectGroundTruth evaluates a bounded candidate window and enforces aggregate scoring limits.
func collectGroundTruth(ctx context.Context, repoDir string, win *prWindow, candidates []commitStamp) (*GroundTruthResult, error) {
	result := &GroundTruthResult{
		MergeCommit:  win.mergeSHA,
		PRCommits:    win.commits,
		LaterCommits: len(candidates),
	}
	for i := 0; i < len(candidates) && i < MaxCommitScan; i++ {
		fix, matched, evalErr := evaluateCandidateCommit(ctx, repoDir, win, candidates[i])
		if evalErr != nil {
			return nil, evalErr
		}
		if matched {
			result.Fixes = append(result.Fixes, fix)
			hunks, err := appendUniqueHunks(result.Hunks, fix.Hunks)
			if err != nil {
				return nil, err
			}
			result.Hunks = hunks
		}
	}
	return result, nil
}

// resolveHistoricalWindow refuses a PR whose merge does not belong to the selected cutoff tree.
func resolveHistoricalWindow(ctx context.Context, repoDir, baseName string, win *prWindow, cutoff time.Time) error {
	var err error
	win.cutoffSHA, err = Cutoff(ctx, repoDir, baseName, cutoff)
	if err != nil {
		return err
	}
	return verifyPRAtCutoff(ctx, repoDir, win.mergeSHA, win.cutoffSHA)
}

// verifyPRAtCutoff requires the replayed PR merge to belong to the actual selected tree.
func verifyPRAtCutoff(ctx context.Context, repoDir, mergeSHA, cutoffSHA string) error {
	present, err := isAncestor(ctx, repoDir, mergeSHA, cutoffSHA)
	if err != nil {
		return err
	}
	if !present {
		return refusedf(ErrNoGroundTruth, "PR commit %s is absent from cutoff %s", mergeSHA, cutoffSHA)
	}
	return nil
}

// validateGroundTruthInputs checks caller arguments before running git probes.
func validateGroundTruthInputs(ctx context.Context, repoDir string, pr PRSpec, cutoff, until time.Time) error {
	if ctx == nil {
		return errors.New("ground truth requires a context")
	}
	if strings.TrimSpace(repoDir) == "" || strings.TrimSpace(pr.Merge) == "" {
		return errors.New("ground truth requires repository path and PR merge commit or range")
	}
	if cutoff.IsZero() || until.IsZero() || until.Before(cutoff) {
		return errors.New("ground truth requires nonzero cutoff and until timestamps with until >= cutoff")
	}
	return nil
}

// resolvePRWindow resolves PRSpec.Merge (commit or start..end range) and computes PR patch IDs.
func resolvePRWindow(ctx context.Context, repoDir, baseName, baseTip string, pr PRSpec) (*prWindow, error) {
	prBase, mergeSHA, commits, err := resolvePRCommits(ctx, repoDir, pr.Merge)
	if err != nil {
		return nil, err
	}
	onBase, err := isAncestor(ctx, repoDir, mergeSHA, baseTip)
	if err != nil {
		return nil, err
	}
	if !onBase {
		return nil, fmt.Errorf("%w: merge commit %s is not on %s", ErrRefused, mergeSHA, baseName)
	}
	win := &prWindow{
		number:   pr.Number,
		baseSHA:  prBase,
		mergeSHA: mergeSHA,
		commits:  commits,
		set:      make(map[string]bool, len(commits)),
		patchIDs: make(map[string]bool, len(commits)+1),
	}
	for i := 0; i < len(commits) && i < MaxCommitScan; i++ {
		win.set[commits[i]] = true
	}
	if err := populatePRMetadata(ctx, repoDir, win); err != nil {
		return nil, err
	}
	return win, nil
}

// resolvePRCommits resolves either a "base..head" range or a single commit/merge SHA.
func resolvePRCommits(ctx context.Context, repoDir, mergeSpec string) (string, string, []string, error) {
	if strings.Contains(mergeSpec, "...") {
		return "", "", nil, fmt.Errorf("%w: symmetric range %q is not supported; use start..end", ErrRefused, mergeSpec)
	}
	if left, right, isRange := strings.Cut(mergeSpec, ".."); isRange {
		return resolvePRRange(ctx, repoDir, left, right)
	}
	mergeSHA, err := util.ResolveGitCommit(ctx, repoDir, mergeSpec)
	if err != nil || mergeSHA == "" {
		return "", "", nil, fmt.Errorf("%w: cannot resolve PR commit %q", ErrRefused, mergeSpec)
	}
	parents, err := commitParents(ctx, repoDir, mergeSHA)
	if err != nil {
		return "", "", nil, err
	}
	if len(parents) == 0 {
		return "", "", nil, fmt.Errorf("%w: PR commit %s has no parent commit", ErrRefused, mergeSHA)
	}
	if len(parents) == 1 {
		return parents[0], mergeSHA, []string{mergeSHA}, nil
	}
	commits, err := revListBounded(ctx, repoDir, parents[0]+".."+mergeSHA)
	if err != nil {
		return "", "", nil, err
	}
	return parents[0], mergeSHA, commits, nil
}

// resolvePRRange resolves an explicit base..head range into base OID, head OID, and commit list.
func resolvePRRange(ctx context.Context, repoDir, left, right string) (string, string, []string, error) {
	baseSHA, err := util.ResolveGitCommit(ctx, repoDir, left)
	if err != nil || baseSHA == "" {
		return "", "", nil, fmt.Errorf("%w: invalid range start %q", ErrRefused, left)
	}
	headSHA, err := util.ResolveGitCommit(ctx, repoDir, right)
	if err != nil || headSHA == "" {
		return "", "", nil, fmt.Errorf("%w: invalid range end %q", ErrRefused, right)
	}
	ancestor, err := isAncestor(ctx, repoDir, baseSHA, headSHA)
	if err != nil {
		return "", "", nil, err
	}
	if !ancestor {
		return "", "", nil, fmt.Errorf("%w: PR range start is not an ancestor of its end", ErrRefused)
	}
	commits, err := revListBounded(ctx, repoDir, baseSHA+".."+headSHA)
	if err != nil {
		return "", "", nil, err
	}
	if len(commits) == 0 {
		return "", "", nil, fmt.Errorf("%w: empty PR range %s..%s", ErrRefused, left, right)
	}
	return baseSHA, headSHA, commits, nil
}

// populatePRMetadata checks the PR diff line bound and records patch IDs and subjects.
func populatePRMetadata(ctx context.Context, repoDir string, win *prWindow) error {
	prDiff, err := readUnifiedZeroDiff(ctx, repoDir, win.baseSHA, win.mergeSHA)
	if err != nil {
		return err
	}
	if _, err := parseDiffHunksBounded(prDiff); err != nil {
		return err
	}
	if id, err := computePatchID(ctx, repoDir, prDiff); err != nil {
		return err
	} else if id != "" {
		win.patchIDs[id] = true
	}
	for i := 0; i < len(win.commits) && i < MaxCommitScan; i++ {
		if err := recordSinglePRCommit(ctx, repoDir, win, win.commits[i]); err != nil {
			return err
		}
	}
	return nil
}

// recordSinglePRCommit records the subject and per-commit patch ID of one PR commit.
func recordSinglePRCommit(ctx context.Context, repoDir string, win *prWindow, sha string) error {
	parents, subject, err := commitParentsAndSubject(ctx, repoDir, sha)
	if err != nil {
		return err
	}
	if subject != "" {
		win.subjects = append(win.subjects, subject)
	}
	if len(parents) == 0 {
		return nil
	}
	diffBytes, err := readUnifiedZeroDiff(ctx, repoDir, parents[0], sha)
	if err != nil {
		return err
	}
	id, err := computePatchID(ctx, repoDir, diffBytes)
	if err != nil {
		return err
	}
	if id != "" {
		win.patchIDs[id] = true
	}
	return nil
}

// listLaterCommits returns first-parent commits on base after mergeSHA within (cutoff, until].
func listLaterCommits(ctx context.Context, repoDir, mergeSHA, baseTip string, cutoff, until time.Time) ([]commitStamp, error) {
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes,
		"log", "--first-parent", "--reverse", "--format=%H%x09%P%x09%ct",
		fmt.Sprintf("-n%d", MaxCommitScan+1), "--since-as-filter="+strconv.FormatInt(cutoff.Unix()+1, 10),
		"--until="+strconv.FormatInt(until.Unix(), 10), mergeSHA+".."+baseTip, "--")
	if err != nil {
		return nil, fmt.Errorf("list later commits %s..%s: %w", mergeSHA, baseTip, err)
	}
	stamps, err := parseCommitStamps(string(res.Stdout), true)
	if err != nil {
		return nil, err
	}
	filtered := make([]commitStamp, 0, len(stamps))
	for i := 0; i < len(stamps) && i < maxScanLines; i++ {
		if !stamps[i].time.After(cutoff) {
			continue
		}
		if stamps[i].time.After(until) {
			continue
		}
		filtered = append(filtered, stamps[i])
		if len(filtered) > MaxCommitScan {
			return nil, refusedf(ErrLimitExceeded, "post-merge window exceeds %d commits", MaxCommitScan)
		}
	}
	return filtered, nil
}

// evaluateCandidateCommit checks diff bounds, excludes patch-id re-lands, and matches fixes.
func evaluateCandidateCommit(ctx context.Context, repoDir string, win *prWindow, cand commitStamp) (FixCommit, bool, error) {
	if len(cand.parents) == 0 {
		return FixCommit{}, false, nil
	}
	parent := cand.parents[0]
	diffBytes, err := readUnifiedZeroDiff(ctx, repoDir, parent, cand.sha)
	if err != nil {
		return FixCommit{}, false, err
	}
	hunks, err := parseDiffHunksBounded(diffBytes)
	if err != nil {
		return FixCommit{}, false, err
	}
	reland, err := matchesPRPatch(ctx, repoDir, win, diffBytes)
	if err != nil {
		return FixCommit{}, false, err
	}
	if reland {
		return FixCommit{}, false, nil
	}
	msg, err := candidateMessages(ctx, repoDir, parent, cand)
	if err != nil {
		return FixCommit{}, false, err
	}
	msgReason := matchCommitMessage(msg, win.number, win.commits, win.subjects)
	blamed, blameErr := blamePRHunks(ctx, repoDir, parent, hunks, win.set)
	if blameErr != nil {
		return FixCommit{}, false, blameErr
	}
	fix, matched, err := buildFixMatch(cand.sha, msgReason, blamed, hunks)
	if err != nil || !matched {
		return fix, matched, err
	}
	fix.Hunks, err = mapFixHunksToCutoff(ctx, repoDir, win.cutoffSHA, parent, fix.Hunks)
	return fix, matched, err
}

// matchesPRPatch identifies patch-equivalent re-lands before message or blame matching.
func matchesPRPatch(ctx context.Context, repoDir string, win *prWindow, diff []byte) (bool, error) {
	id, err := computePatchID(ctx, repoDir, diff)
	return id != "" && win.patchIDs[id], err
}

// buildFixMatch combines message and blame signals into a FixCommit when either matched.
func buildFixMatch(sha, msgReason string, blamed []FixHunk, allHunks []parsedHunk) (FixCommit, bool, error) {
	if msgReason == "" && len(blamed) == 0 {
		return FixCommit{}, false, nil
	}
	reason := msgReason
	if reason == "" {
		reason = "blame"
	}
	fixHunks := append([]FixHunk(nil), blamed...)
	if msgReason != "" {
		for i := 0; i < len(allHunks) && i < MaxDiffLines; i++ {
			var err error
			fixHunks, err = appendUniqueHunks(fixHunks, []FixHunk{allHunks[i].toFixHunk()})
			if err != nil {
				return FixCommit{}, false, err
			}
		}
	}
	return FixCommit{Commit: sha, Reason: reason, Hunks: fixHunks}, true, nil
}

// matchCommitMessage checks PR numbers, commit prefixes, exact Reverts, and normalized subjects.
func matchCommitMessage(msg string, prNumber int, prCommits, prSubjects []string) string {
	if prNumber > 0 && mentionsPRNumber(msg, prNumber) {
		return fmt.Sprintf("pr #%d", prNumber)
	}
	if prefix := mentionsCommitPrefix(msg, prCommits); prefix != "" {
		return "commit " + prefix
	}
	for i := 0; i < len(prSubjects) && i < MaxCommitScan; i++ {
		if prSubjects[i] != "" && strings.Contains(msg, fmt.Sprintf("Revert %q", prSubjects[i])) {
			return "revert subject"
		}
	}
	return matchSubjectMessage(msg, prSubjects)
}

// minSubjectLength excludes short, generic subjects after normalization.
const minSubjectLength = 16

var (
	conventionalSubjectPrefix = regexp.MustCompile(`^[a-z][a-z0-9-]*(\([^()]*\))?!?:\s*`)
	subjectPRSuffix           = regexp.MustCompile(`\s+(\(#[0-9]+\)|#[0-9]+)$`)
)

// matchSubjectMessage matches sufficiently descriptive subjects within a later commit message.
func matchSubjectMessage(msg string, prSubjects []string) string {
	message := strings.Join(strings.Fields(strings.ToLower(msg)), " ")
	for i := 0; i < len(prSubjects) && i < MaxCommitScan; i++ {
		subject := normalizeRepairSubject(prSubjects[i])
		if utf8.RuneCountInString(subject) >= minSubjectLength && strings.Contains(message, subject) {
			return "subject"
		}
	}
	return ""
}

// normalizeRepairSubject folds case and whitespace, stripping conventional prefixes and PR suffixes.
func normalizeRepairSubject(subject string) string {
	subject = strings.Join(strings.Fields(strings.ToLower(subject)), " ")
	subject = conventionalSubjectPrefix.ReplaceAllString(subject, "")
	return subjectPRSuffix.ReplaceAllString(subject, "")
}

// mentionsPRNumber checks word-bounded references such as "#42", "PR 42", "Fixes #42", or "Revert #42".
func mentionsPRNumber(msg string, prNumber int) bool {
	num := strconv.Itoa(prNumber)
	lower := strings.ToLower(msg)
	prefixes := []string{"#", "pr #", "pr ", "pull request #", "pull request ", "fixes #", "fixes: #", "revert #"}
	for p := 0; p < len(prefixes); p++ {
		if containsBoundedNumber(lower, prefixes[p], num) {
			return true
		}
	}
	return false
}

// containsBoundedNumber reports whether text contains prefix+digits bounded by non-word/non-digit edges.
func containsBoundedNumber(text, prefix, digits string) bool {
	needle := prefix + digits
	offset := 0
	for step := 0; step < maxScanLines && offset <= len(text)-len(needle); step++ {
		idx := strings.Index(text[offset:], needle)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		end := pos + len(needle)
		leftOK := pos == 0 || !isAlphaNum(text[pos-1])
		rightOK := end == len(text) || !isDigit(text[end])
		if leftOK && rightOK {
			return true
		}
		offset = pos + 1
	}
	return false
}

// mentionsCommitPrefix returns the first hexadecimal token >= MinCommitPrefix matching a PR commit.
func mentionsCommitPrefix(msg string, prCommits []string) string {
	for i := 0; i < len(msg) && i < maxGitProbeBytes; i++ {
		token, next, ok := hexTokenAt(msg, i)
		i = next
		if ok && matchesAnyPrefix(token, prCommits) {
			return token
		}
	}
	return ""
}

// hexTokenAt reads a word-bounded hexadecimal token starting at msg[start].
func hexTokenAt(msg string, start int) (string, int, bool) {
	if !isHexByte(msg[start]) || (start > 0 && isAlphaNum(msg[start-1])) {
		return "", start, false
	}
	end := start
	for end < len(msg) && end-start < 64 && isHexByte(msg[end]) {
		end++
	}
	if end < len(msg) && isAlphaNum(msg[end]) {
		return "", end, false
	}
	token := strings.ToLower(msg[start:end])
	return token, end, len(token) >= MinCommitPrefix
}

// matchesAnyPrefix reports whether hexToken is a prefix of any full commit SHA in commits.
func matchesAnyPrefix(hexToken string, commits []string) bool {
	for i := 0; i < len(commits) && i < MaxCommitScan; i++ {
		if strings.HasPrefix(strings.ToLower(commits[i]), hexToken) {
			return true
		}
	}
	return false
}
