// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// LegacyClaimComment identifies a legacy claim comment on an issue.
type LegacyClaimComment struct {
	IssueNumber int
	CommentID   int64
	Claim       Claim
	Takeover    string
}

// MigratedHolder records the holder state (tagged session and stage) of an issue for migration readback comparison.
type MigratedHolder struct {
	SessionTag string
	Stage      string
	None       bool
}

// MigrateCounts records the counts of migrated and skipped claims for an issue.
type MigrateCounts struct {
	Migrated         int
	SkippedOtherHost int
}

// ValidClaimSession reports whether session is 1..128 characters of letters, digits and . _ / : + @ -
func ValidClaimSession(session string) bool {
	return claimValuePattern.MatchString(session)
}

// ValidateClaimSession checks whether session matches the allowed claim session format.
func ValidateClaimSession(session string) error {
	if !claimValuePattern.MatchString(session) {
		return fmt.Errorf("claim session %q is not 1..128 characters of letters, digits and . _ / : + @ -", session)
	}
	return nil
}

// ReadbackVerifier verifies the post-migration forge state.
type ReadbackVerifier func(ctx context.Context, f ClaimForge, issueNumber int, oldIDs []int64, newIDs []int64, beforeHolder MigratedHolder, key []byte) error

func extractClaimNote(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "- Note: "); ok {
			return rest
		}
	}
	return ""
}

func reportProtectedNoteDrop(ctx context.Context, out io.Writer, text string) {
	if text == "" || out == nil {
		return
	}
	err := DefaultPublicTextGuard().Check(ctx, text)
	if err == nil {
		return
	}
	var prot *ProtectedTextError
	if errors.As(err, &prot) {
		if _, writeErr := fmt.Fprintf(out, "note dropped: carries a protected %s\n", prot.Kind); writeErr != nil {
			return
		}
		return
	}
	if _, writeErr := fmt.Fprintf(out, "note dropped: carries a protected text\n"); writeErr != nil {
		return
	}
}

func filterAndReportProtectedNote(ctx context.Context, out io.Writer, text string) string {
	if text == "" {
		return ""
	}
	err := DefaultPublicTextGuard().Check(ctx, text)
	if err == nil {
		return text
	}
	reportProtectedNoteDrop(ctx, out, text)
	return ""
}

// FindLegacyClaims parses issue comments and returns all legacy claim comments in comment order.
// A comment whose first line is a claim marker with a plain (untagged) session or lane is a legacy claim.
// Comments that are not claims are ignored.
func FindLegacyClaims(issueNumber int, comments []IssueComment) []LegacyClaimComment {
	var legacy []LegacyClaimComment
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		c := comments[i]
		claim, ok := ParseClaimMarker(c.Body)
		if !ok {
			continue
		}
		if !IsClaimTag(claim.Session) || !IsClaimTag(claim.Lane) {
			claim.CommentID = c.ID
			claim.Note = extractClaimNote(c.Body)
			legacy = append(legacy, LegacyClaimComment{
				IssueNumber: issueNumber,
				CommentID:   c.ID,
				Claim:       claim,
				Takeover:    takeoverNote(c.Body),
			})
		}
	}
	return legacy
}

func printLegacyClaimDryRun(ctx context.Context, out io.Writer, key []byte, l LegacyClaimComment) error {
	sessionTag := l.Claim.Session
	if !IsClaimTag(sessionTag) {
		sessionTag = ClaimTag(key, "session", sessionTag)
	}
	laneTag := l.Claim.Lane
	if !IsClaimTag(laneTag) {
		laneTag = ClaimTag(key, "lane", laneTag)
	}
	if _, err := fmt.Fprintf(out, "issue %d comment %d: stage %s, session %s, lane %s\n",
		l.IssueNumber, l.CommentID, l.Claim.Stage, sessionTag, laneTag); err != nil {
		return err
	}
	reportProtectedNoteDrop(ctx, out, l.Claim.Note)
	if l.Takeover != "" {
		reportProtectedNoteDrop(ctx, out, l.Takeover)
	}
	return nil
}

func migrateOneLegacyComment(ctx context.Context, f ClaimForge, key []byte, l LegacyClaimComment, out io.Writer) (int64, error) {
	claim := l.Claim
	claim.Note = filterAndReportProtectedNote(ctx, out, claim.Note)
	takeover := filterAndReportProtectedNote(ctx, out, l.Takeover)

	newBody, err := renderClaimBodyWithKey(claim, takeover, key)
	if err != nil {
		return 0, fmt.Errorf("render tagged claim for comment %d: %w", l.CommentID, err)
	}

	newComment, err := f.CreateIssueComment(ctx, l.IssueNumber, newBody)
	if err != nil {
		return 0, fmt.Errorf("create migrated comment for %d: %w", l.CommentID, err)
	}

	if err := f.DeleteIssueComment(ctx, l.CommentID); err != nil {
		return newComment.ID, fmt.Errorf("delete legacy comment %d failed after posting new comment %d: %w",
			l.CommentID, newComment.ID, err)
	}
	return newComment.ID, nil
}

func computeIssueHolder(comments []IssueComment, key []byte) MigratedHolder {
	var (
		bestID int64
		best   MigratedHolder
		found  bool
	)
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		c, ok := ParseClaimMarker(comments[i].Body)
		if !ok || c.Released() {
			continue
		}
		if !found || comments[i].ID < bestID {
			sessionTag := c.Session
			if !IsClaimTag(sessionTag) {
				sessionTag = ClaimTag(key, "session", sessionTag)
			}
			best = MigratedHolder{
				SessionTag: sessionTag,
				Stage:      c.Stage,
				None:       false,
			}
			bestID = comments[i].ID
			found = true
		}
	}
	if !found {
		return MigratedHolder{None: true}
	}
	return best
}

func checkMigratedLegacyRemoved(issueNumber int, comments []IssueComment, oldIDs []int64) error {
	migrated := make(map[int64]bool, len(oldIDs))
	for i := 0; i < len(oldIDs) && i < MaxIssueCommentsLimit; i++ {
		migrated[oldIDs[i]] = true
	}
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		if migrated[comments[i].ID] {
			return fmt.Errorf("issue #%d: legacy claim marker %d still present after migration",
				issueNumber, comments[i].ID)
		}
	}
	return nil
}

func checkNewCommentsPresent(issueNumber int, comments []IssueComment, newIDs []int64) error {
	present := make(map[int64]bool, len(comments))
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		present[comments[i].ID] = true
	}
	for i := 0; i < len(newIDs) && i < MaxIssueCommentsLimit; i++ {
		if !present[newIDs[i]] {
			return fmt.Errorf("issue #%d: new claim comment %d missing on read-back", issueNumber, newIDs[i])
		}
	}
	return nil
}

func verifyMigratedReadback(ctx context.Context, f ClaimForge, issueNumber int, oldIDs []int64, newIDs []int64, beforeHolder MigratedHolder, key []byte) error {
	comments, err := f.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return fmt.Errorf("read-back issue #%d comments: %w", issueNumber, err)
	}
	if err := checkMigratedLegacyRemoved(issueNumber, comments, oldIDs); err != nil {
		return err
	}
	if err := checkNewCommentsPresent(issueNumber, comments, newIDs); err != nil {
		return err
	}
	afterHolder := computeIssueHolder(comments, key)
	if beforeHolder != afterHolder {
		return fmt.Errorf("issue #%d: claim holder changed during migration: before=%+v, after=%+v",
			issueNumber, beforeHolder, afterHolder)
	}
	return nil
}

// isLegacyClaim reports whether a claim uses untagged legacy session or lane format.
func isLegacyClaim(c Claim) bool {
	return !IsClaimTag(c.Session) || !IsClaimTag(c.Lane)
}

// checkReorderingStep validates one comment against any prior unreleased legacy claim.
func checkReorderingStep(issueNumber int, comment IssueComment, migratingIDs map[int64]bool, legacySeen *bool, legacyID *int64) error {
	c, ok := ParseClaimMarker(comment.Body)
	if !ok || c.Released() {
		return nil
	}
	isLegacy := isLegacyClaim(c)
	isMigrating := migratingIDs[comment.ID]
	if isLegacy && isMigrating {
		*legacySeen = true
		*legacyID = comment.ID
		return nil
	}
	if *legacySeen && !isMigrating {
		kind := "skipped"
		if !isLegacy {
			kind = "tagged"
		}
		return fmt.Errorf("issue #%d: %s unreleased claim comment %d sits after legacy unreleased claim comment %d",
			issueNumber, kind, comment.ID, *legacyID)
	}
	return nil
}

func checkLegacyReordering(issueNumber int, comments []IssueComment, migratingIDs map[int64]bool) error {
	var (
		legacyUnreleasedSeen bool
		legacyUnreleasedID   int64
	)
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		if err := checkReorderingStep(issueNumber, comments[i], migratingIDs, &legacyUnreleasedSeen, &legacyUnreleasedID); err != nil {
			return err
		}
	}
	return nil
}

func shouldMigrateClaim(claim Claim, isClosed bool, ownSessions []string) bool {
	if claim.Released() || claim.Stage == ClaimStageReleased {
		return true
	}
	if isClosed {
		return true
	}
	return slices.Contains(ownSessions, claim.Session)
}

func reportSkippedForeignClaim(out io.Writer, issueNumber int, commentID int64, stage string) error {
	if out == nil {
		return nil
	}
	_, err := fmt.Fprintf(out, "issue #%d comment %d: held by a session of another host (stage %s); migrate it from that host\n",
		issueNumber, commentID, stage)
	return err
}

func partitionLegacyClaims(legacy []LegacyClaimComment, isClosed bool, ownSessions []string) ([]LegacyClaimComment, []LegacyClaimComment, map[int64]bool) {
	var toMigrate []LegacyClaimComment
	var toSkip []LegacyClaimComment
	migratingIDs := make(map[int64]bool, len(legacy))
	for i := 0; i < len(legacy) && i < MaxIssueCommentsLimit; i++ {
		l := legacy[i]
		if shouldMigrateClaim(l.Claim, isClosed, ownSessions) {
			toMigrate = append(toMigrate, l)
			migratingIDs[l.CommentID] = true
		} else {
			toSkip = append(toSkip, l)
		}
	}
	return toMigrate, toSkip, migratingIDs
}

func reportIssueClaims(ctx context.Context, out io.Writer, key []byte, legacy []LegacyClaimComment, migratingIDs map[int64]bool, apply bool) error {
	for i := 0; i < len(legacy) && i < MaxIssueCommentsLimit; i++ {
		l := legacy[i]
		if migratingIDs[l.CommentID] {
			if !apply {
				if err := printLegacyClaimDryRun(ctx, out, key, l); err != nil {
					return err
				}
			}
		} else {
			if err := reportSkippedForeignClaim(out, l.IssueNumber, l.CommentID, l.Claim.Stage); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyLegacyMigration(ctx context.Context, f ClaimForge, issueNumber int, key []byte, out io.Writer, legacy []LegacyClaimComment, beforeHolder MigratedHolder, verifier ReadbackVerifier) error {
	newIDs := make([]int64, 0, len(legacy))
	oldIDs := make([]int64, 0, len(legacy))
	for i := 0; i < len(legacy) && i < MaxIssueCommentsLimit; i++ {
		l := legacy[i]
		newID, err := migrateOneLegacyComment(ctx, f, key, l, out)
		if err != nil {
			return err
		}
		newIDs = append(newIDs, newID)
		oldIDs = append(oldIDs, l.CommentID)
	}
	if verifier != nil {
		return verifier(ctx, f, issueNumber, oldIDs, newIDs, beforeHolder, key)
	}
	return nil
}

func resolveKey(key []byte) ([]byte, error) {
	if len(key) == 0 {
		k, err := LoadOrCreateDefaultClaimKey()
		if err != nil {
			return nil, fmt.Errorf("load claim key: %w", err)
		}
		return k, nil
	}
	return key, nil
}

func handleReorderingRefusal(out io.Writer, apply bool, reorderErr error) error {
	if apply {
		return fmt.Errorf("%w: migration refused", reorderErr)
	}
	if out != nil {
		if _, writeErr := fmt.Fprintln(out, reorderErr.Error()+": migration refused"); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func executeLegacyMigration(ctx context.Context, f ClaimForge, issueNumber int, key []byte, out io.Writer, toMigrate []LegacyClaimComment, comments []IssueComment, verifier ReadbackVerifier) error {
	beforeHolder := computeIssueHolder(comments, key)
	return applyLegacyMigration(ctx, f, issueNumber, key, out, toMigrate, beforeHolder, verifier)
}

// MigrateIssueClaims migrates legacy claims on an issue to keyed HMAC-SHA256 identity tags.
// Dry run (apply=false) lists legacy claims and computed tags without changing the forge.
// Apply (apply=true) posts a tagged comment, deletes the old comment, and verifies readback.
func MigrateIssueClaims(ctx context.Context, f ClaimForge, issueNumber int, apply bool, key []byte, ownSessions []string, out io.Writer) (MigrateCounts, error) {
	return MigrateIssueClaimsWithVerifier(ctx, f, issueNumber, apply, key, ownSessions, out, verifyMigratedReadback)
}

// MigrateIssueClaimsWithVerifier allows injecting a custom readback verifier for testing mutants.
func MigrateIssueClaimsWithVerifier(ctx context.Context, f ClaimForge, issueNumber int, apply bool, key []byte, ownSessions []string, out io.Writer, verifier ReadbackVerifier) (MigrateCounts, error) {
	resolvedKey, err := resolveKey(key)
	if err != nil {
		return MigrateCounts{}, err
	}
	comments, err := f.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return MigrateCounts{}, fmt.Errorf("list issue #%d comments: %w", issueNumber, err)
	}
	legacy := FindLegacyClaims(issueNumber, comments)
	if len(legacy) == 0 {
		return MigrateCounts{}, nil
	}
	issue, err := f.GetIssue(ctx, issueNumber)
	if err != nil {
		return MigrateCounts{}, fmt.Errorf("get issue #%d: %w", issueNumber, err)
	}
	toMigrate, toSkip, migratingIDs := partitionLegacyClaims(legacy, issue.State == "closed", ownSessions)
	if reorderErr := checkLegacyReordering(issueNumber, comments, migratingIDs); reorderErr != nil {
		return MigrateCounts{}, handleReorderingRefusal(out, apply, reorderErr)
	}
	if err := reportIssueClaims(ctx, out, resolvedKey, legacy, migratingIDs, apply); err != nil {
		return MigrateCounts{}, err
	}
	counts := MigrateCounts{Migrated: len(toMigrate), SkippedOtherHost: len(toSkip)}
	if !apply || len(toMigrate) == 0 {
		return counts, nil
	}
	if err := executeLegacyMigration(ctx, f, issueNumber, resolvedKey, out, toMigrate, comments, verifier); err != nil {
		return MigrateCounts{}, err
	}
	return counts, nil
}
