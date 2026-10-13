// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

// Tests for #1292: a claim refuses an issue that is not open, records whether it assigned the
// account, and release removes only an assignee the claim added.

func releaseClaim(t *testing.T, d *forge.ClaimDesk, session, outcome string) (forge.ClaimResult, error) {
	t.Helper()
	return d.Release(context.Background(), testRef(), forge.ReleaseRequest{Session: session, Outcome: outcome})
}

// ownClaim reads the claim of the first claim comment back from the fake.
func ownClaim(t *testing.T, f *forgetest.ClaimFake) (forge.Claim, string) {
	t.Helper()
	for _, c := range f.Comments {
		if claim, ok := forge.ParseClaimMarker(c.Body); ok {
			return claim, c.Body
		}
	}
	t.Fatalf("no claim comment in %+v", f.Comments)
	return forge.Claim{}, ""
}

// seedAuthored adds a claim comment written by author, as the account that claimed it.
func seedAuthored(t *testing.T, f *forgetest.ClaimFake, c forge.Claim, author string) {
	t.Helper()
	marker, err := forge.RenderClaimMarker(c)
	if err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	f.NextID++
	f.Comments = append(f.Comments, forge.IssueComment{ID: f.NextID, Author: author, Association: "OWNER", Body: marker + "\nhuman text\n"})
}

func TestClaim_Negative_ClosedIssueIsRefusedNamingItsState(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	f.State = "closed"
	_, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), forge.ClaimRequest{Session: "s1", Lane: "agy", Branch: "feat/s1"})
	if !errors.Is(err, forge.ErrIssueNotOpen) || !strings.Contains(err.Error(), "acme/widgets#7 is closed") {
		t.Fatalf("claim on a closed issue must be refused naming its state, got %v", err)
	}
	for _, write := range []string{"CreateIssueComment", "EditIssueComment", "EnsureLabel", "AddLabels", "AddAssignees"} {
		if slices.Contains(f.Calls, write) {
			t.Fatalf("refused claim wrote to the forge (%s): %v", write, f.Calls)
		}
	}
	if len(f.Comments) != 0 || len(f.Assignees) != 0 || len(f.OnIssue) != 0 {
		t.Fatalf("refused claim left state: comments=%d assignees=%v labels=%v", len(f.Comments), f.Assignees, f.OnIssue)
	}
}

func TestClaim_Boundary_OnlyTheOpenStateIsClaimable(t *testing.T) {
	cases := map[string]string{
		"":       "acme/widgets#7 is in no reported state",
		"OPEN":   "acme/widgets#7 is OPEN",
		"opened": "acme/widgets#7 is opened",
	}
	for state, want := range cases {
		f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
		f.State = state
		_, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), forge.ClaimRequest{Session: "s1", Lane: "agy", Branch: "feat/s1"})
		if !errors.Is(err, forge.ErrIssueNotOpen) || !strings.Contains(err.Error(), want) {
			t.Errorf("state %q: want refusal naming %q, got %v", state, want, err)
		}
	}
}

func TestRelease_Positive_RemovesTheAssigneeTheClaimAdded(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	claim, body := ownClaim(t, f)
	if claim.Assignee != forge.ClaimAssigneeAdded || !strings.Contains(body, "- Assignee: added by this claim; release removes it") {
		t.Fatalf("claim must record that it added the assignee: %+v\n%s", claim, body)
	}
	if _, err := releaseClaim(t, d, "s1", "abandoned"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(f.Assignees) != 0 || f.OnIssue[forge.LabelInProgress] {
		t.Fatalf("release left assignee %v or labels %v", f.Assignees, f.OnIssue)
	}
	if claim, _ := ownClaim(t, f); !claim.Released() || claim.Assignee != forge.ClaimAssigneeAdded {
		t.Fatalf("released marker must keep the assignee case: %+v", claim)
	}
}

func TestRelease_Positive_ClosedIssueStillReleasesAndUnassigns(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	f.State = "closed" // the pull request closed the issue
	if _, err := releaseClaim(t, d, "s1", "landed"); err != nil {
		t.Fatalf("release of a closed issue must succeed: %v", err)
	}
	if len(f.Assignees) != 0 {
		t.Fatalf("landed release left the assignee: %v", f.Assignees)
	}
}

func TestRelease_Boundary_KeepsAnAssigneeFromBeforeTheClaim(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	f.Assignees = []string{"Operator"} // logins compare without case, as on GitHub
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	claim, body := ownClaim(t, f)
	if claim.Assignee != forge.ClaimAssigneeBefore || !strings.Contains(body, "- Assignee: assigned before this claim; release keeps it") {
		t.Fatalf("claim must record the earlier assignment: %+v\n%s", claim, body)
	}
	if _, err := releaseClaim(t, d, "s1", "abandoned"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if slices.Contains(f.Calls, "RemoveAssignees") || !slices.Contains(f.Assignees, "Operator") {
		t.Fatalf("release removed an assignment made before the claim: calls=%v assignees=%v", f.Calls, f.Assignees)
	}
}

func TestRelease_Boundary_KeepsOtherAssignees(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	f.Assignees = []string{"alice"}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	if _, err := releaseClaim(t, d, "s1", "handed-over"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !slices.Equal(f.Assignees, []string{"alice"}) {
		t.Fatalf("release must remove only the claiming account: %v", f.Assignees)
	}
}

func TestRelease_Boundary_MarkerWithoutAssigneeCaseKeepsAssignees(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	f.Assignees = []string{"operator"}
	seedAuthored(t, f, claimAt("s1", claimEpoch), "operator") // written before the record existed
	if _, err := releaseClaim(t, newTestDesk(f, clock), "s1", "abandoned"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if slices.Contains(f.Calls, "RemoveAssignees") || len(f.Assignees) != 1 {
		t.Fatalf("a claim without the record must leave assignees: calls=%v assignees=%v", f.Calls, f.Assignees)
	}
}

func TestRelease_Negative_UnassignFailureKeepsTheClaimReleasable(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	f.FailOn = "RemoveAssignees"
	if _, err := releaseClaim(t, d, "s1", "abandoned"); !errors.Is(err, forge.ErrClaimUnverifiable) {
		t.Fatalf("a failed unassign must fail the release, got %v", err)
	}
	if claim, _ := ownClaim(t, f); claim.Released() {
		t.Fatalf("a failed unassign must leave the claim live for a retry: %+v", claim)
	}
	f.FailOn = ""
	if _, err := releaseClaim(t, d, "s1", "abandoned"); err != nil {
		t.Fatalf("retried release: %v", err)
	}
	if len(f.Assignees) != 0 {
		t.Fatalf("retried release left the assignee: %v", f.Assignees)
	}
}

func TestClaim_Boundary_ResumeKeepsTheRecordedCase(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	clock.now = clock.now.Add(time.Hour)
	if res := mustClaim(t, d, "s1"); res.Action != "resumed" {
		t.Fatalf("second claim must resume: %+v", res)
	}
	if claim, _ := ownClaim(t, f); claim.Assignee != forge.ClaimAssigneeAdded {
		t.Fatalf("resume must keep the case the first claim recorded, though the account is assigned now: %+v", claim)
	}
	if _, err := releaseClaim(t, d, "s1", "abandoned"); err != nil || len(f.Assignees) != 0 {
		t.Fatalf("release after resume: err=%v assignees=%v", err, f.Assignees)
	}
}

func TestClaim_Boundary_TakeoverInheritsOnlyTheSameAccountsAssignment(t *testing.T) {
	cases := []struct {
		author, want string
	}{
		{author: "operator", want: forge.ClaimAssigneeAdded},
		{author: "someone", want: forge.ClaimAssigneeBefore},
	}
	for _, tc := range cases {
		f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
		f.Assignees = []string{"operator"}
		stale := claimAt("s1", claimEpoch.Add(-7*time.Hour))
		stale.Assignee = forge.ClaimAssigneeAdded
		seedAuthored(t, f, stale, tc.author)
		if res := mustClaim(t, newTestDesk(f, clock), "s2"); res.Action != "took-over" || res.Claim.Assignee != tc.want {
			t.Errorf("stale claim by %s: want took-over with case %q, got %+v", tc.author, tc.want, res)
		}
	}
}

func TestClaimMarker_AssigneeRoundTripAndShape(t *testing.T) {
	c := claimAt("s1", claimEpoch)
	c.Assignee = forge.ClaimAssigneeBefore
	marker, err := forge.RenderClaimMarker(c)
	if err != nil || !strings.Contains(marker, " assignee=before") {
		t.Fatalf("render: %q %v", marker, err)
	}
	if got, ok := forge.ParseClaimMarker(marker); !ok || got.Assignee != forge.ClaimAssigneeBefore {
		t.Fatalf("round trip: %+v %v", got, ok)
	}
	c.Assignee = "maybe"
	if _, err := forge.RenderClaimMarker(c); err == nil {
		t.Fatal("an unknown assignee case must not render")
	}
	base := strings.TrimSuffix(strings.Replace(marker, " assignee=before", "", 1), " -->")
	for _, bad := range []string{base + " assignee=maybe -->", base + " assignee=claim assignee=claim -->", base + " assignee= -->"} {
		if _, ok := forge.ParseClaimMarker(bad); ok {
			t.Errorf("marker %q must not parse", bad)
		}
	}
}
