// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// ReadbackVerifier verifies the post-migration forge state.
type ReadbackVerifier func(ctx context.Context, f ClaimForge, issueNumber int, newIDs []int64, beforeHolder MigratedHolder, key []byte) error

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

func checkNoLegacyMarkers(issueNumber int, comments []IssueComment) error {
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		if c, ok := ParseClaimMarker(comments[i].Body); ok {
			if !IsClaimTag(c.Session) || !IsClaimTag(c.Lane) {
				return fmt.Errorf("issue #%d: legacy claim marker %d still present after migration",
					issueNumber, comments[i].ID)
			}
		}
	}
	return nil
}

func checkNewCommentsPresent(issueNumber int, comments []IssueComment, newIDs []int64) error {
	present := make(map[int64]bool, len(comments))
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		present[comments[i].ID] = true
	}
	for _, id := range newIDs {
		if !present[id] {
			return fmt.Errorf("issue #%d: new claim comment %d missing on read-back", issueNumber, id)
		}
	}
	return nil
}

func verifyMigratedReadback(ctx context.Context, f ClaimForge, issueNumber int, newIDs []int64, beforeHolder MigratedHolder, key []byte) error {
	comments, err := f.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return fmt.Errorf("read-back issue #%d comments: %w", issueNumber, err)
	}
	if err := checkNoLegacyMarkers(issueNumber, comments); err != nil {
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

func checkLegacyReordering(issueNumber int, comments []IssueComment) error {
	var (
		legacyUnreleasedSeen bool
		legacyUnreleasedID   int64
	)
	for i := 0; i < len(comments) && i < MaxIssueCommentsLimit; i++ {
		c, ok := ParseClaimMarker(comments[i].Body)
		if !ok || c.Released() {
			continue
		}
		isLegacy := !IsClaimTag(c.Session) || !IsClaimTag(c.Lane)
		if isLegacy {
			legacyUnreleasedSeen = true
			legacyUnreleasedID = comments[i].ID
		} else if legacyUnreleasedSeen {
			return fmt.Errorf("issue #%d: tagged unreleased claim comment %d sits after legacy unreleased claim comment %d",
				issueNumber, comments[i].ID, legacyUnreleasedID)
		}
	}
	return nil
}

func applyLegacyMigration(ctx context.Context, f ClaimForge, issueNumber int, key []byte, out io.Writer, legacy []LegacyClaimComment, beforeHolder MigratedHolder, verifier ReadbackVerifier) error {
	newIDs := make([]int64, 0, len(legacy))
	for _, l := range legacy {
		newID, err := migrateOneLegacyComment(ctx, f, key, l, out)
		if err != nil {
			return err
		}
		newIDs = append(newIDs, newID)
	}
	if verifier != nil {
		return verifier(ctx, f, issueNumber, newIDs, beforeHolder, key)
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

func dryRunLegacyMigration(ctx context.Context, out io.Writer, key []byte, legacy []LegacyClaimComment) error {
	for _, l := range legacy {
		if err := printLegacyClaimDryRun(ctx, out, key, l); err != nil {
			return err
		}
	}
	return nil
}

// MigrateIssueClaims migrates legacy claims on an issue to keyed HMAC-SHA256 identity tags.
// Dry run (apply=false) lists legacy claims and computed tags without changing the forge.
// Apply (apply=true) posts a tagged comment, deletes the old comment, and verifies readback.
func MigrateIssueClaims(ctx context.Context, f ClaimForge, issueNumber int, apply bool, key []byte, out io.Writer) error {
	return MigrateIssueClaimsWithVerifier(ctx, f, issueNumber, apply, key, out, verifyMigratedReadback)
}

// MigrateIssueClaimsWithVerifier allows injecting a custom readback verifier for testing mutants.
func MigrateIssueClaimsWithVerifier(ctx context.Context, f ClaimForge, issueNumber int, apply bool, key []byte, out io.Writer, verifier ReadbackVerifier) error {
	resolvedKey, err := resolveKey(key)
	if err != nil {
		return err
	}
	comments, err := f.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return fmt.Errorf("list issue #%d comments: %w", issueNumber, err)
	}
	if reorderErr := checkLegacyReordering(issueNumber, comments); reorderErr != nil {
		if !apply {
			if out != nil {
				if _, writeErr := fmt.Fprintln(out, reorderErr.Error()+": migration refused"); writeErr != nil {
					return writeErr
				}
			}
			return nil
		}
		return fmt.Errorf("%w: migration refused", reorderErr)
	}
	legacy := FindLegacyClaims(issueNumber, comments)
	if !apply {
		return dryRunLegacyMigration(ctx, out, resolvedKey, legacy)
	}
	if len(legacy) == 0 {
		return nil
	}
	beforeHolder := computeIssueHolder(comments, resolvedKey)
	return applyLegacyMigration(ctx, f, issueNumber, resolvedKey, out, legacy, beforeHolder, verifier)
}
