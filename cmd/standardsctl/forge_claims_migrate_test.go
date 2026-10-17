// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

func useFakeClaimsMigrate(t *testing.T, fake *forgetest.ClaimFake) *bytes.Buffer {
	t.Helper()
	previousOpener, previousOutput := claimsMigrateOpener, claimOutput
	var out bytes.Buffer
	claimOutput = &out
	claimsMigrateOpener = func(string, string) forge.ClaimForgeOpener {
		return func(string, string) (forge.ClaimForge, error) {
			return fake, nil
		}
	}
	t.Cleanup(func() {
		claimsMigrateOpener, claimOutput = previousOpener, previousOutput
	})
	return &out
}

func seedCLILegacyComment(fake *forgetest.ClaimFake, issueNum int, session, lane, branch, stage string) int64 {
	return seedCLILegacyCommentWithNote(fake, issueNum, session, lane, branch, stage, "")
}

func seedCLILegacyCommentWithNote(fake *forgetest.ClaimFake, issueNum int, session, lane, branch, stage, note string) int64 {
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

func TestForgeClaimsMigrateCLI_Positive_DryRun(t *testing.T) {
	fake := forgetest.NewClaimFake()
	out := useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "plain-session-123", "plain-lane-abc", "feat/cli", "implementing")
	origLen := len(fake.Comments)

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=plain-session-123", "--token=test-tok"})
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	if len(fake.Comments) != origLen {
		t.Fatalf("dry run changed comments: %+v", fake.Comments)
	}

	outStr := out.String()
	if strings.Contains(outStr, "plain-session-123") || strings.Contains(outStr, "plain-lane-abc") {
		t.Fatalf("dry run output leaked plain identity: %s", outStr)
	}
	if !strings.Contains(outStr, fmt.Sprintf("issue 7 comment %d: stage implementing", oldID)) {
		t.Fatalf("dry run output missing expected line: %s", outStr)
	}
}

func TestForgeClaimsMigrateCLI_Positive_Apply(t *testing.T) {
	fake := forgetest.NewClaimFake()
	useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "plain-session-456", "plain-lane-def", "feat/cli-apply", "review")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=plain-session-456", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Verify old comment deleted and new comment created
	var foundNew bool
	for _, c := range fake.Comments {
		if c.ID == oldID {
			t.Fatalf("legacy comment %d was not deleted", oldID)
		}
		claim, ok := forge.ParseClaimMarker(c.Body)
		if ok && forge.IsClaimTag(claim.Session) && forge.IsClaimTag(claim.Lane) {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("expected new tagged claim comment on fake forge, got: %+v", fake.Comments)
	}
}

func TestForgeClaimsMigrateCLI_Positive_MultipleIssues(t *testing.T) {
	fake := forgetest.NewClaimFake()
	useFakeClaimsMigrate(t, fake)

	seedCLILegacyComment(fake, 7, "s1", "l1", "feat/a", "claimed")
	seedCLILegacyComment(fake, 8, "s2", "l2", "feat/b", "implementing")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--issue=8", "--own-session=s1", "--own-session=s2", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("multiple issues migrate failed: %v", err)
	}

	for _, c := range fake.Comments {
		claim, ok := forge.ParseClaimMarker(c.Body)
		if !ok || !forge.IsClaimTag(claim.Session) || !forge.IsClaimTag(claim.Lane) {
			t.Fatalf("expected all claims to be tagged: %s", c.Body)
		}
	}
}

func TestForgeClaimsMigrateCLI_Negative_FlagValidation(t *testing.T) {
	fake := forgetest.NewClaimFake()
	useFakeClaimsMigrate(t, fake)

	cases := map[string][]string{
		"missing repo":    {"claims", "migrate", "--issue=7", "--token=x"},
		"bare repo":       {"claims", "migrate", "--repo=widgets", "--issue=7", "--token=x"},
		"missing issue":   {"claims", "migrate", "--repo=acme/widgets", "--token=x"},
		"invalid issue":   {"claims", "migrate", "--repo=acme/widgets", "--issue=abc", "--token=x"},
		"negative issue":  {"claims", "migrate", "--repo=acme/widgets", "--issue=-1", "--token=x"},
		"zero issue":      {"claims", "migrate", "--repo=acme/widgets", "--issue=0", "--token=x"},
		"apply w/o token": {"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--apply"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			// No token may come from the host: no token variables, and no gh on PATH whose
			// signed-in session would answer gh auth token.
			gitOnlyPath(t)
			if err := dispatchCommand("forge", args); err == nil {
				t.Fatalf("case %s: expected error", name)
			}
		})
	}
}

func TestForgeClaimsMigrateCLI_Positive_ProtectedNoteDropped_DryRunAndApply(t *testing.T) {
	forge.DefaultPublicTextGuard().SetPrivateNames([]string{"cli-classified"})
	t.Cleanup(func() {
		forge.DefaultPublicTextGuard().SetPrivateNames(nil)
	})

	// 1. Dry run output: names "note dropped: carries a protected private name", never the secret
	fakeDry := forgetest.NewClaimFake()
	outDry := useFakeClaimsMigrate(t, fakeDry)
	seedCLILegacyCommentWithNote(fakeDry, 7, "s1", "l1", "feat/dry", "implementing", "work on cli-classified")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=s1", "--token=test-tok"})
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	dryStr := outDry.String()
	if !strings.Contains(dryStr, "note dropped: carries a protected private name") {
		t.Fatalf("dry run missing drop message, got: %s", dryStr)
	}
	if strings.Contains(dryStr, "cli-classified") {
		t.Fatalf("dry run leaked protected value: %s", dryStr)
	}

	// 2. Apply output: prints drop message and removes note from newly created comment
	fakeApply := forgetest.NewClaimFake()
	outApply := useFakeClaimsMigrate(t, fakeApply)
	seedCLILegacyCommentWithNote(fakeApply, 7, "s1", "l1", "feat/apply", "implementing", "work on cli-classified")

	err = dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=s1", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	applyStr := outApply.String()
	if !strings.Contains(applyStr, "note dropped: carries a protected private name") {
		t.Fatalf("apply missing drop message, got: %s", applyStr)
	}
	if strings.Contains(applyStr, "cli-classified") {
		t.Fatalf("apply leaked protected value: %s", applyStr)
	}
	if len(fakeApply.Comments) != 1 {
		t.Fatalf("expected 1 migrated comment, got %d", len(fakeApply.Comments))
	}
	if strings.Contains(fakeApply.Comments[0].Body, "cli-classified") || strings.Contains(fakeApply.Comments[0].Body, "- Note:") {
		t.Fatalf("migrated comment still carries note: %s", fakeApply.Comments[0].Body)
	}
}

func TestForgeClaimsMigrateCLI_Negative_TaggedUnreleasedAfterLegacyRefusedUpfront(t *testing.T) {
	fake := forgetest.NewClaimFake()
	out := useFakeClaimsMigrate(t, fake)

	legacyID := seedCLILegacyComment(fake, 7, "plain-s1", "plain-l1", "feat/legacy", "implementing")

	fake.NextID++
	taggedID := fake.NextID
	taggedMarker, err := forge.RenderClaimMarkerWithKey(forge.Claim{
		Session: "session-2",
		Lane:    "lane-2",
		Branch:  "feat/tagged",
		Stage:   "implementing",
		Started: time.Now().Add(-time.Hour),
		Updated: time.Now(),
	}, fake.Key)
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
	err = dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=plain-s1", "--token=test-tok"})
	if err != nil {
		t.Fatalf("dry run should not return error: %v", err)
	}
	dryStr := out.String()
	if !strings.Contains(dryStr, "migration refused") {
		t.Fatalf("dry run missing 'migration refused': %s", dryStr)
	}
	if !strings.Contains(dryStr, fmt.Sprintf("tagged unreleased claim comment %d sits after legacy unreleased claim comment %d", taggedID, legacyID)) {
		t.Fatalf("dry run does not name conflicting comment IDs: %s", dryStr)
	}

	// 2. Apply returns an error
	err = dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=plain-s1", "--apply", "--token=test-tok"})
	if err == nil {
		t.Fatal("expected apply to return error, got nil")
	}
	if !strings.Contains(err.Error(), "migration refused") {
		t.Fatalf("apply error should contain 'migration refused': %v", err)
	}
}

// Positive: released claim of a foreign session migrates.
func TestForgeClaimsMigrateCLI_Positive_ForeignReleasedMigrates(t *testing.T) {
	fake := forgetest.NewClaimFake()
	out := useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "foreign-sess-rel", "plain-lane", "feat/rel", "released")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("released claim migration failed: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "migrated=1 skipped-other-host=0") {
		t.Fatalf("output missing migrated=1 count: %s", outStr)
	}

	var foundNew bool
	for _, c := range fake.Comments {
		if c.ID == oldID {
			t.Fatalf("old comment %d not deleted", oldID)
		}
		claim, ok := forge.ParseClaimMarker(c.Body)
		if ok && forge.IsClaimTag(claim.Session) && forge.IsClaimTag(claim.Lane) {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatal("expected new tagged comment on fake forge")
	}
}

// Positive: live claim (stage review) of a foreign session on an open issue is skipped and no forge write happens.
func TestForgeClaimsMigrateCLI_Positive_LiveForeignClaimOpenIssueSkippedNoWrite(t *testing.T) {
	fake := forgetest.NewClaimFake()
	out := useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "foreign-sess-rev", "foreign-lane-42", "feat/cli-foreign", "review")
	origLen := len(fake.Comments)

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("skipped claim command returned error: %v", err)
	}

	// Verify no forge writes happened
	for _, call := range fake.Calls {
		if call == "CreateIssueComment" || call == "DeleteIssueComment" || call == "EditIssueComment" {
			t.Fatalf("forge write %q recorded during skipped claim migration", call)
		}
	}
	if len(fake.Comments) != origLen || fake.Comments[0].ID != oldID {
		t.Fatalf("comments changed during skipped claim migration: %+v", fake.Comments)
	}

	outStr := out.String()
	wantSkip := fmt.Sprintf("issue #7 comment %d: held by a session of another host (stage review); migrate it from that host", oldID)
	if !strings.Contains(outStr, wantSkip) {
		t.Fatalf("output missing skip line %q, got: %s", wantSkip, outStr)
	}
	// Verify skip line holds no session or lane name
	if strings.Contains(outStr, "foreign-sess-rev") || strings.Contains(outStr, "foreign-lane-42") {
		t.Fatalf("skip line leaked foreign session or lane: %s", outStr)
	}
	if !strings.Contains(outStr, "migrated=0 skipped-other-host=1") {
		t.Fatalf("output missing skipped count: %s", outStr)
	}
}

// Positive: a live foreign claim on a closed issue migrates.
func TestForgeClaimsMigrateCLI_Positive_LiveForeignClaimClosedIssueMigrates(t *testing.T) {
	fake := forgetest.NewClaimFake()
	fake.State = "closed"
	out := useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "foreign-sess-closed", "plain-lane", "feat/closed", "review")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("closed issue claim migration failed: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "migrated=1 skipped-other-host=0") {
		t.Fatalf("output missing migrated=1 count: %s", outStr)
	}

	var foundNew bool
	for _, c := range fake.Comments {
		if c.ID == oldID {
			t.Fatalf("old comment %d not deleted", oldID)
		}
		claim, ok := forge.ParseClaimMarker(c.Body)
		if ok && forge.IsClaimTag(claim.Session) && forge.IsClaimTag(claim.Lane) {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatal("expected new tagged comment on fake forge")
	}
}

// Positive: the same live claim with --own-session naming its session migrates.
func TestForgeClaimsMigrateCLI_Positive_LiveForeignClaimWithOwnSessionMigrates(t *testing.T) {
	fake := forgetest.NewClaimFake()
	out := useFakeClaimsMigrate(t, fake)

	oldID := seedCLILegacyComment(fake, 7, "worker-session-1", "plain-lane", "feat/worker", "review")

	err := dispatchCommand("forge", []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=worker-session-1", "--apply", "--token=test-tok"})
	if err != nil {
		t.Fatalf("own-session migration failed: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "migrated=1 skipped-other-host=0") {
		t.Fatalf("output missing migrated=1 count: %s", outStr)
	}

	var foundNew bool
	for _, c := range fake.Comments {
		if c.ID == oldID {
			t.Fatalf("old comment %d not deleted", oldID)
		}
		claim, ok := forge.ParseClaimMarker(c.Body)
		if ok && forge.IsClaimTag(claim.Session) && forge.IsClaimTag(claim.Lane) {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatal("expected new tagged comment on fake forge")
	}
}

// Negative: --own-session with 65 names refused.
func TestForgeClaimsMigrateCLI_Negative_OwnSession65Refused(t *testing.T) {
	args := []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--token=test-tok"}
	for i := 1; i <= 65; i++ {
		args = append(args, fmt.Sprintf("--own-session=sess%d", i))
	}
	err := dispatchCommand("forge", args)
	if err == nil {
		t.Fatal("expected error with 65 own-session names, got nil")
	}
	if !strings.Contains(err.Error(), "64") && !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// Negative: an invalid name refused.
func TestForgeClaimsMigrateCLI_Negative_OwnSessionInvalidRefused(t *testing.T) {
	args := []string{"claims", "migrate", "--repo=acme/widgets", "--issue=7", "--own-session=?invalid!", "--token=test-tok"}
	err := dispatchCommand("forge", args)
	if err == nil {
		t.Fatal("expected error with invalid own-session name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --own-session") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
