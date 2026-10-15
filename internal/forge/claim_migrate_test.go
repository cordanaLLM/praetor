// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

func testClaimKey() []byte {
	return bytes.Repeat([]byte{0x42}, forge.ClaimKeySize)
}

func seedLegacyComment(t *testing.T, fake *forgetest.ClaimFake, issueNum int, session, lane, branch, stage, note string) int64 {
	t.Helper()
	fake.NextID++
	id := fake.NextID
	startTime := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	updateTime := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	marker := fmt.Sprintf("<!-- praetor-claim v=1 session=%s lane=%s branch=%s started=%s updated=%s stage=%s assignee=claim -->\n",
		session, lane, branch, startTime.Format(time.RFC3339), updateTime.Format(time.RFC3339), stage)
	body := marker + fmt.Sprintf("**Praetor claim** - session `%s`, lane `%s`, branch `%s`\n\n- Stage: %s\n- Started: %s\n- Updated: %s\n- Assignee: added by this claim; release removes it\n",
		session, lane, branch, stage, startTime.Format(time.RFC3339), updateTime.Format(time.RFC3339))
	if note != "" {
		body += "- Note: " + note + "\n"
	}
	fake.Comments = append(fake.Comments, forge.IssueComment{
		ID:          id,
		Author:      "operator",
		Association: "OWNER",
		Body:        body,
	})
	return id
}

// Positive: a legacy comment migrates to tagged values with same times, stage and branch,
// while old comment is deleted and non-claim comments stay untouched.
func TestClaimsMigrate_Positive_LegacyCommentMigrates(t *testing.T) {
	fake := forgetest.NewClaimFake()
	key := testClaimKey()
	ctx := context.Background()

	// Seed one non-claim comment and one legacy claim comment
	fake.Comments = append(fake.Comments, forge.IssueComment{
		ID:          50,
		Author:      "user1",
		Association: "NONE",
		Body:        "Just a normal user issue discussion comment",
	})
	oldID := seedLegacyComment(t, fake, 7, "plain-session-alpha", "plain-lane-one", "feat/migrate", "implementing", "progress note")

	var out bytes.Buffer
	if err := forge.MigrateIssueClaims(ctx, fake, 7, true, key, &out); err != nil {
		t.Fatalf("migrate legacy claim: %v", err)
	}

	// Non-claim comment must stay untouched
	var foundNonClaim bool
	var newClaimComment *forge.IssueComment
	for i := range fake.Comments {
		if fake.Comments[i].ID == 50 {
			foundNonClaim = true
			if fake.Comments[i].Body != "Just a normal user issue discussion comment" {
				t.Fatalf("non-claim comment modified: %q", fake.Comments[i].Body)
			}
		}
		if fake.Comments[i].ID != 50 && fake.Comments[i].ID != oldID {
			newClaimComment = &fake.Comments[i]
		}
	}

	if !foundNonClaim {
		t.Fatal("non-claim comment was removed")
	}
	if newClaimComment == nil {
		t.Fatal("expected new claim comment to be posted")
	}

	claim, ok := forge.ParseClaimMarker(newClaimComment.Body)
	if !ok {
		t.Fatalf("new comment marker invalid: %s", newClaimComment.Body)
	}

	// Verify tagged values
	wantSessionTag := forge.ClaimTag(key, "session", "plain-session-alpha")
	wantLaneTag := forge.ClaimTag(key, "lane", "plain-lane-one")
	if claim.Session != wantSessionTag {
		t.Errorf("session tag = %q, want %q", claim.Session, wantSessionTag)
	}
	if claim.Lane != wantLaneTag {
		t.Errorf("lane tag = %q, want %q", claim.Lane, wantLaneTag)
	}
	if claim.Branch != "feat/migrate" || claim.Stage != "implementing" {
		t.Errorf("branch/stage mismatch: %s / %s", claim.Branch, claim.Stage)
	}
	if !strings.Contains(newClaimComment.Body, "- Note: progress note") {
		t.Errorf("note was not preserved: %s", newClaimComment.Body)
	}
	if strings.Contains(newClaimComment.Body, "plain-session-alpha") || strings.Contains(newClaimComment.Body, "plain-lane-one") {
		t.Errorf("new comment leaks plain identity values: %s", newClaimComment.Body)
	}

	// Verify old comment was deleted
	for _, c := range fake.Comments {
		if c.ID == oldID {
			t.Fatalf("old legacy comment %d was not deleted", oldID)
		}
	}
}

// Positive: dry run prints issue, comment id, stage and computed tags without changing the forge
// and never prints plain values.
func TestClaimsMigrate_Positive_DryRunChangesNothing(t *testing.T) {
	fake := forgetest.NewClaimFake()
	key := testClaimKey()
	ctx := context.Background()

	oldID := seedLegacyComment(t, fake, 7, "secret-session-99", "secret-lane-42", "feat/dry", "review", "")
	origComments := append([]forge.IssueComment(nil), fake.Comments...)

	var out bytes.Buffer
	if err := forge.MigrateIssueClaims(ctx, fake, 7, false, key, &out); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	if len(fake.Comments) != len(origComments) || fake.Comments[0].ID != oldID {
		t.Fatalf("dry run must not change comments: %+v", fake.Comments)
	}

	wantSessionTag := forge.ClaimTag(key, "session", "secret-session-99")
	wantLaneTag := forge.ClaimTag(key, "lane", "secret-lane-42")
	outStr := out.String()

	if strings.Contains(outStr, "secret-session-99") || strings.Contains(outStr, "secret-lane-42") {
		t.Fatalf("dry run leaked plain value in output: %s", outStr)
	}
	if !strings.Contains(outStr, fmt.Sprintf("issue 7 comment %d: stage review, session %s, lane %s", oldID, wantSessionTag, wantLaneTag)) {
		t.Fatalf("dry run output missing expected line: %s", outStr)
	}
}

// Positive: non-claim comments are untouched during migration.
func TestClaimsMigrate_Positive_NonClaimCommentStays(t *testing.T) {
	fake := forgetest.NewClaimFake()
	key := testClaimKey()
	ctx := context.Background()

	fake.Comments = []forge.IssueComment{
		{ID: 101, Author: "bob", Association: "MEMBER", Body: "Issue discussion 1"},
		{ID: 102, Author: "carol", Association: "NONE", Body: "Issue discussion 2"},
	}

	var out bytes.Buffer
	if err := forge.MigrateIssueClaims(ctx, fake, 7, true, key, &out); err != nil {
		t.Fatalf("migrate on non-claims failed: %v", err)
	}

	if len(fake.Comments) != 2 || fake.Comments[0].ID != 101 || fake.Comments[1].ID != 102 {
		t.Fatalf("non-claim comments must not be changed: %+v", fake.Comments)
	}
}

// Negative: a delete failure after comment creation is reported with both comment IDs and stops command.
func TestClaimsMigrate_Negative_DeleteFailureReportedAndStops(t *testing.T) {
	fake := forgetest.NewClaimFake()
	fake.FailOn = "DeleteIssueComment"
	key := testClaimKey()
	ctx := context.Background()

	oldID := seedLegacyComment(t, fake, 7, "s1", "l1", "feat/x", "claimed", "")

	var out bytes.Buffer
	err := forge.MigrateIssueClaims(ctx, fake, 7, true, key, &out)
	if err == nil {
		t.Fatal("expected error on DeleteIssueComment failure")
	}

	// Verify both comment IDs are named
	errStr := err.Error()
	if !strings.Contains(errStr, fmt.Sprintf("legacy comment %d", oldID)) {
		t.Errorf("error %q does not name old comment id %d", errStr, oldID)
	}
	var newID int64
	for _, c := range fake.Comments {
		if c.ID != oldID {
			newID = c.ID
			break
		}
	}
	if newID == 0 {
		t.Fatal("expected new comment to have been created before delete failed")
	}
	if !strings.Contains(errStr, fmt.Sprintf("new comment %d", newID)) {
		t.Errorf("error %q does not name new comment id %d", errStr, newID)
	}
}

// nonDeletingFake embeds ClaimFake and ignores DeleteIssueComment calls, keeping the old comment.
type nonDeletingFake struct {
	*forgetest.ClaimFake
}

func (n *nonDeletingFake) DeleteIssueComment(context.Context, int64) error {
	// Silently succeed without deleting the comment
	return nil
}

// Mutant verification: when read-back is skipped, a delete failure that leaves the legacy marker
// passes without an error. With read-back, it fails.
func TestClaimsMigrate_Negative_MutantSkipsReadback(t *testing.T) {
	key := testClaimKey()
	ctx := context.Background()

	// 1. With normal read-back: non-deleting fake fails because legacy marker remains.
	rawFake1 := forgetest.NewClaimFake()
	seedLegacyComment(t, rawFake1, 7, "plain-session", "plain-lane", "feat/x", "claimed", "")
	fake1 := &nonDeletingFake{ClaimFake: rawFake1}

	var out1 bytes.Buffer
	err1 := forge.MigrateIssueClaims(ctx, fake1, 7, true, key, &out1)
	if err1 == nil {
		t.Fatal("expected error with read-back when legacy comment remains")
	}
	if !strings.Contains(err1.Error(), "still present after migration") {
		t.Fatalf("unexpected error message: %v", err1)
	}

	// 2. Mutant: verifier is nil (read-back skipped) -> passes without error!
	rawFake2 := forgetest.NewClaimFake()
	seedLegacyComment(t, rawFake2, 7, "plain-session", "plain-lane", "feat/x", "claimed", "")
	fake2 := &nonDeletingFake{ClaimFake: rawFake2}

	var out2 bytes.Buffer
	err2 := forge.MigrateIssueClaimsWithVerifier(ctx, fake2, 7, true, key, &out2, nil)
	if err2 != nil {
		t.Fatalf("mutant skipping read-back was expected to pass without error, got: %v", err2)
	}
}

func TestClaimsMigrate_Positive_ProtectedNoteDropped_DryRunAndApply(t *testing.T) {
	key := testClaimKey()
	ctx := context.Background()

	forge.DefaultPublicTextGuard().SetPrivateNames([]string{"classified-codename"})
	t.Cleanup(func() {
		forge.DefaultPublicTextGuard().SetPrivateNames(nil)
	})

	// 1. Dry run output: names "note dropped: carries a protected private name", never the secret
	fakeDry := forgetest.NewClaimFake()
	seedLegacyComment(t, fakeDry, 7, "s1", "l1", "feat/dry", "implementing", "work on classified-codename")

	var dryOut bytes.Buffer
	if err := forge.MigrateIssueClaims(ctx, fakeDry, 7, false, key, &dryOut); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	dryStr := dryOut.String()
	if !strings.Contains(dryStr, "note dropped: carries a protected private name") {
		t.Fatalf("dry run missing drop message, got: %s", dryStr)
	}
	if strings.Contains(dryStr, "classified-codename") {
		t.Fatalf("dry run leaked protected value: %s", dryStr)
	}

	// 2. Apply output: prints "note dropped: carries a protected private name", posts comment without note
	fakeApply := forgetest.NewClaimFake()
	seedLegacyComment(t, fakeApply, 7, "s1", "l1", "feat/apply", "implementing", "work on classified-codename")

	var applyOut bytes.Buffer
	if err := forge.MigrateIssueClaims(ctx, fakeApply, 7, true, key, &applyOut); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	applyStr := applyOut.String()
	if !strings.Contains(applyStr, "note dropped: carries a protected private name") {
		t.Fatalf("apply missing drop message, got: %s", applyStr)
	}
	if strings.Contains(applyStr, "classified-codename") {
		t.Fatalf("apply output leaked protected value: %s", applyStr)
	}

	// Verify migrated comment on forge dropped the note
	if len(fakeApply.Comments) != 1 {
		t.Fatalf("expected 1 migrated comment, got %d", len(fakeApply.Comments))
	}
	if strings.Contains(fakeApply.Comments[0].Body, "classified-codename") || strings.Contains(fakeApply.Comments[0].Body, "- Note:") {
		t.Fatalf("migrated comment body still carries note: %s", fakeApply.Comments[0].Body)
	}
}

func TestClaimsMigrate_Negative_Planted_TaggedUnreleasedAfterLegacyRefusedUpfront(t *testing.T) {
	fake := forgetest.NewClaimFake()
	key := testClaimKey()
	ctx := context.Background()

	// Seed legacy unreleased claim comment 1
	legacyID := seedLegacyComment(t, fake, 7, "plain-s1", "plain-l1", "feat/legacy", "implementing", "")

	// Seed tagged unreleased claim comment 2 (after legacy unreleased claim)
	fake.NextID++
	taggedID := fake.NextID
	taggedMarker, err := forge.RenderClaimMarkerWithKey(forge.Claim{
		Session: "session-2",
		Lane:    "lane-2",
		Branch:  "feat/tagged",
		Stage:   "implementing",
		Started: time.Now().Add(-time.Hour),
		Updated: time.Now(),
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	fake.Comments = append(fake.Comments, forge.IssueComment{
		ID:          taggedID,
		Author:      "operator",
		Association: "OWNER",
		Body:        taggedMarker + "\n**Praetor claim**\n",
	})

	// 1. Dry run names the refusal and comment IDs
	var dryOut bytes.Buffer
	err = forge.MigrateIssueClaims(ctx, fake, 7, false, key, &dryOut)
	if err != nil {
		t.Fatalf("dry run should not return error: %v", err)
	}
	dryStr := dryOut.String()
	if !strings.Contains(dryStr, "migration refused") {
		t.Fatalf("dry run missing 'migration refused': %s", dryStr)
	}
	if !strings.Contains(dryStr, fmt.Sprintf("tagged unreleased claim comment %d sits after legacy unreleased claim comment %d", taggedID, legacyID)) {
		t.Fatalf("dry run does not name the conflicting comment IDs: %s", dryStr)
	}

	// 2. Apply refuses the issue up front with an error
	var applyOut bytes.Buffer
	err = forge.MigrateIssueClaims(ctx, fake, 7, true, key, &applyOut)
	if err == nil {
		t.Fatal("expected apply to refuse migration up front, got nil")
	}
	if !strings.Contains(err.Error(), "migration refused") {
		t.Fatalf("apply error should contain 'migration refused': %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("tagged unreleased claim comment %d sits after legacy unreleased claim comment %d", taggedID, legacyID)) {
		t.Fatalf("apply error does not name the conflicting comment IDs: %v", err)
	}
}

type holderMutatingFake struct {
	*forgetest.ClaimFake
}

func (m *holderMutatingFake) CreateIssueComment(ctx context.Context, issueNumber int, body string) (forge.IssueComment, error) {
	// Mutate body so stage becomes review instead of implementing
	body = strings.Replace(body, "stage=implementing", "stage=review", 1)
	return m.ClaimFake.CreateIssueComment(ctx, issueNumber, body)
}

func TestClaimsMigrate_Negative_Planted_ReadbackFailsWhenHolderChanges(t *testing.T) {
	key := testClaimKey()
	ctx := context.Background()

	rawFake := forgetest.NewClaimFake()
	seedLegacyComment(t, rawFake, 7, "plain-session", "plain-lane", "feat/x", "implementing", "")
	fake := &holderMutatingFake{ClaimFake: rawFake}

	var out bytes.Buffer
	err := forge.MigrateIssueClaims(ctx, fake, 7, true, key, &out)
	if err == nil {
		t.Fatal("expected read-back to fail when holder changed during migration")
	}
	if !strings.Contains(err.Error(), "claim holder changed during migration") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
