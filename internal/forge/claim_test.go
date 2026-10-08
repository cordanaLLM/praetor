// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var claimEpoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// fakeClaimForge is an in-memory forge for one repository. failOn names a method that fails.
type fakeClaimForge struct {
	mu        sync.Mutex
	pr        bool
	comments  []IssueComment
	nextID    int64
	labels    map[string]bool // labels the repository has
	onIssue   map[string]bool // labels on the issue
	assignees []string
	failOn    string
	calls     []string
	// beforeList runs before ListIssueComments answers, to stage a concurrent writer.
	beforeList func(f *fakeClaimForge, call int)
	listCalls  int
}

func newFakeClaimForge() *fakeClaimForge {
	return &fakeClaimForge{nextID: 100, labels: map[string]bool{}, onIssue: map[string]bool{}}
}

func (f *fakeClaimForge) record(name string) error {
	f.calls = append(f.calls, name)
	if f.failOn == name {
		return fmt.Errorf("fake forge: %s failed", name)
	}
	return nil
}

func (f *fakeClaimForge) GetIssue(_ context.Context, number int) (IssueSpec, error) {
	if err := f.record("GetIssue"); err != nil {
		return IssueSpec{}, err
	}
	if f.pr {
		return IssueSpec{}, errors.New("fake forge: number is a pull request")
	}
	return IssueSpec{ID: number}, nil
}

func (f *fakeClaimForge) Viewer(context.Context) (string, error) {
	if err := f.record("Viewer"); err != nil {
		return "", err
	}
	return "operator", nil
}

func (f *fakeClaimForge) ListIssueComments(context.Context, int) ([]IssueComment, error) {
	f.listCalls++
	if f.beforeList != nil {
		f.beforeList(f, f.listCalls)
	}
	if err := f.record("ListIssueComments"); err != nil {
		return nil, err
	}
	return append([]IssueComment(nil), f.comments...), nil
}

func (f *fakeClaimForge) CreateIssueComment(_ context.Context, _ int, body string) (IssueComment, error) {
	if err := f.record("CreateIssueComment"); err != nil {
		return IssueComment{}, err
	}
	f.nextID++
	c := IssueComment{ID: f.nextID, Author: "operator", Association: "OWNER", Body: body}
	f.comments = append(f.comments, c)
	return c, nil
}

func (f *fakeClaimForge) EditIssueComment(_ context.Context, id int64, body string) error {
	if err := f.record("EditIssueComment"); err != nil {
		return err
	}
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments[i].Body = body
			return nil
		}
	}
	return fmt.Errorf("fake forge: no comment %d", id)
}

func (f *fakeClaimForge) EnsureLabel(_ context.Context, l Label) error {
	if err := f.record("EnsureLabel"); err != nil {
		return err
	}
	f.labels[l.Name] = true
	return nil
}

func (f *fakeClaimForge) AddLabels(_ context.Context, _ int, labels []string) error {
	if err := f.record("AddLabels"); err != nil {
		return err
	}
	for _, l := range labels {
		if !f.labels[l] {
			return fmt.Errorf("fake forge: label %s does not exist", l)
		}
		f.onIssue[l] = true
	}
	return nil
}

func (f *fakeClaimForge) RemoveLabel(_ context.Context, _ int, label string) error {
	if err := f.record("RemoveLabel"); err != nil {
		return err
	}
	delete(f.onIssue, label)
	return nil
}

func (f *fakeClaimForge) AddAssignees(_ context.Context, _ int, logins []string) error {
	if err := f.record("AddAssignees"); err != nil {
		return err
	}
	f.assignees = append(f.assignees, logins...)
	return nil
}

// seed adds a comment as a trusted author.
func (f *fakeClaimForge) seed(c Claim, association string) int64 {
	f.nextID++
	marker, err := RenderClaimMarker(c)
	if err != nil {
		panic(err)
	}
	f.comments = append(f.comments, IssueComment{ID: f.nextID, Author: "someone", Association: association, Body: marker + "\nhuman text\n"})
	return f.nextID
}

type deskClock struct{ now time.Time }

func (c *deskClock) Now() time.Time { return c.now }

func newTestDesk(f *fakeClaimForge, clock *deskClock) *ClaimDesk {
	return &ClaimDesk{
		Open:  func(string, string) (ClaimForge, error) { return f, nil },
		Stale: 6 * time.Hour,
		Now:   clock.Now,
	}
}

func testRef() ClaimRef { return ClaimRef{Owner: "acme", Repo: "widgets", Number: 7} }

func claimAt(session string, updated time.Time) Claim {
	return Claim{Session: session, Lane: "agy", Branch: "feat/" + session, Started: updated, Updated: updated, Stage: "implementing"}
}

func mustClaim(t *testing.T, d *ClaimDesk, session string) ClaimResult {
	t.Helper()
	res, err := d.Claim(context.Background(), testRef(), ClaimRequest{Session: session, Lane: "agy", Branch: "feat/" + session})
	if err != nil {
		t.Fatalf("claim by %s: %v", session, err)
	}
	return res
}

func countClaimComments(f *fakeClaimForge) int {
	n := 0
	for _, c := range f.comments {
		if _, ok := ParseClaimMarker(c.Body); ok {
			n++
		}
	}
	return n
}

func TestClaim_Positive_CreatesCommentLabelAndAssignee(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	res := mustClaim(t, newTestDesk(f, clock), "s1")
	if res.Action != "created" || res.Claim.Stage != "claimed" || res.Claim.CommentID == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !f.onIssue[LabelInProgress] || !f.labels[LabelInProgress] {
		t.Fatalf("in-progress label not created and added: repo=%v issue=%v", f.labels, f.onIssue)
	}
	if len(f.assignees) != 1 || f.assignees[0] != "operator" {
		t.Fatalf("account not assigned: %v", f.assignees)
	}
	got, ok := ParseClaimMarker(f.comments[0].Body)
	if !ok || got.Session != "s1" || got.Branch != "feat/s1" || !got.Started.Equal(claimEpoch) {
		t.Fatalf("marker not readable back: %+v ok=%v", got, ok)
	}
}

func TestClaim_Negative_SecondSessionRefusedNamingFirst(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "first")
	clock.now = claimEpoch.Add(time.Hour)
	_, err := d.Claim(context.Background(), testRef(), ClaimRequest{Session: "second", Lane: "codex", Branch: "feat/second"})
	var held *ClaimHeldError
	if !errors.Is(err, ErrClaimHeld) || !errors.As(err, &held) {
		t.Fatalf("second claimer must be refused with ErrClaimHeld, got %v", err)
	}
	if held.Holder.Session != "first" || !strings.Contains(err.Error(), "session first") || !strings.Contains(err.Error(), "feat/first") {
		t.Fatalf("refusal must name the first claim, got %q", err)
	}
	if countClaimComments(f) != 1 {
		t.Fatalf("a refused claim must add no comment, have %d", countClaimComments(f))
	}
}

func TestClaim_Boundary_StaleWindowEdge(t *testing.T) {
	cases := []struct {
		name  string
		age   time.Duration
		stale bool
	}{
		{"one second inside", 6*time.Hour - time.Second, false},
		{"exactly at the window", 6 * time.Hour, false},
		{"one second outside", 6*time.Hour + time.Second, true},
		{"update in the future", -time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
			oldID := f.seed(claimAt("old", claimEpoch.Add(-tc.age)), "OWNER")
			res, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "new", Lane: "agy", Branch: "feat/new"})
			if tc.stale {
				if err != nil || res.Action != "took-over" || res.TookOver == nil || res.TookOver.Session != "old" {
					t.Fatalf("stale claim must be taken over naming the old one: res=%+v err=%v", res, err)
				}
				if res.Claim.CommentID != oldID || countClaimComments(f) != 1 {
					t.Fatalf("takeover must edit the one claim comment, got id %d and %d claim comments", res.Claim.CommentID, countClaimComments(f))
				}
				if !strings.Contains(f.comments[0].Body, "replaces the stale claim of session old") {
					t.Fatalf("comment must name the old claim:\n%s", f.comments[0].Body)
				}
				return
			}
			if !errors.Is(err, ErrClaimHeld) {
				t.Fatalf("claim inside the window must be refused, got res=%+v err=%v", res, err)
			}
		})
	}
}

func TestClaim_Positive_SameSessionResumesWithoutNewComment(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	first := mustClaim(t, d, "s1")
	clock.now = claimEpoch.Add(time.Hour)
	again := mustClaim(t, d, "s1")
	if again.Action != "resumed" || countClaimComments(f) != 1 || !again.Claim.Started.Equal(first.Claim.Started) {
		t.Fatalf("own re-claim must resume the same comment: %+v comments=%d", again, countClaimComments(f))
	}
}

func TestClaim_Positive_ReleasedCommentIsReused(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	if _, err := d.Release(context.Background(), testRef(), ReleaseRequest{Session: "s1", Outcome: "landed"}); err != nil {
		t.Fatal(err)
	}
	res := mustClaim(t, d, "s2")
	if res.Action != "created" || countClaimComments(f) != 1 {
		t.Fatalf("a released claim comment is reused, not duplicated: %+v comments=%d", res, countClaimComments(f))
	}
}

func TestStatus_Positive_EditsTheClaimCommentInPlace(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	clock.now = claimEpoch.Add(30 * time.Minute)
	res, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "review", Note: "round\n one"})
	if err != nil {
		t.Fatal(err)
	}
	if countClaimComments(f) != 1 || len(f.comments) != 1 {
		t.Fatalf("status must edit, not add: %d comments", len(f.comments))
	}
	got, _ := ParseClaimMarker(f.comments[0].Body)
	if got.Stage != "review" || !got.Updated.Equal(clock.now) || !got.Started.Equal(claimEpoch) {
		t.Fatalf("stage and update time not refreshed: %+v", got)
	}
	if res.Action != "updated" || !strings.Contains(f.comments[0].Body, "- Note: round one") {
		t.Fatalf("note not rendered on one line:\n%s", f.comments[0].Body)
	}
}

func TestStatus_Positive_FixRoundAndBlockedLabel(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	for _, stage := range []string{"fix-round-3", "queued", "landing"} {
		if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: stage}); err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
		if f.onIssue[LabelBlocked] {
			t.Fatalf("stage %s must not carry the blocked label", stage)
		}
	}
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "blocked", Note: "3 findings open"}); err != nil {
		t.Fatal(err)
	}
	if !f.onIssue[LabelBlocked] || !f.labels[LabelBlocked] {
		t.Fatalf("blocked stage must create and add %s: %v %v", LabelBlocked, f.labels, f.onIssue)
	}
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "implementing"}); err != nil {
		t.Fatal(err)
	}
	if f.onIssue[LabelBlocked] {
		t.Fatal("leaving blocked must remove the blocked label")
	}
}

func TestStatus_Negative_RefusalsAndBadInput(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "review"}); !errors.Is(err, ErrNoClaim) {
		t.Fatalf("status without a claim must say so, got %v", err)
	}
	mustClaim(t, d, "s1")
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s2", Stage: "review"}); !errors.Is(err, ErrClaimHeld) {
		t.Fatalf("status by a foreign session must be refused, got %v", err)
	}
	for _, stage := range []string{"released", "done", "fix-round-0", "fix-round-1000", "Review"} {
		if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: stage}); err == nil {
			t.Fatalf("stage %q must be refused", stage)
		}
	}
	long := strings.Repeat("x", MaxClaimNoteRunes+1)
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "review", Note: long}); err == nil {
		t.Fatal("an over-long note must be refused, not truncated")
	}
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "review", Note: strings.Repeat("y", MaxClaimNoteRunes)}); err != nil {
		t.Fatalf("a note at the limit must pass: %v", err)
	}
}

func TestStatus_Positive_NoStageReadsWithoutWriting(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	before := f.comments[0].Body
	res, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "other"})
	if err != nil || res.Action != "read" || res.Claim.Session != "s1" || f.comments[0].Body != before {
		t.Fatalf("read must report the holder and write nothing: %+v %v", res, err)
	}
}

func TestStatus_Positive_TakeoverNoteSurvivesEdits(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	f.seed(claimAt("old", claimEpoch.Add(-7*time.Hour)), "OWNER")
	d := newTestDesk(f, clock)
	mustClaim(t, d, "new")
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "new", Stage: "review"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.comments[0].Body, "replaces the stale claim of session old") {
		t.Fatalf("takeover note lost on a status edit:\n%s", f.comments[0].Body)
	}
}

func TestRelease_Positive_FinalisesAndRemovesLabels(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	mustClaim(t, d, "s1")
	if _, err := d.Status(context.Background(), testRef(), StatusRequest{Session: "s1", Stage: "blocked"}); err != nil {
		t.Fatal(err)
	}
	clock.now = claimEpoch.Add(time.Hour)
	res, err := d.Release(context.Background(), testRef(), ReleaseRequest{Session: "s1", Outcome: "handed-over", Note: "to s2"})
	if err != nil || res.Action != "released" {
		t.Fatalf("release: %+v %v", res, err)
	}
	if len(f.onIssue) != 0 {
		t.Fatalf("release must remove both status labels, left %v", f.onIssue)
	}
	got, _ := ParseClaimMarker(f.comments[0].Body)
	if !got.Released() || got.Outcome != "handed-over" || len(f.comments) != 1 {
		t.Fatalf("comment not finalised in place: %+v comments=%d", got, len(f.comments))
	}
	holder, err := d.LiveClaim(context.Background(), testRef())
	if err != nil || holder != nil {
		t.Fatalf("a released issue has no live claim: %v %v", holder, err)
	}
}

func TestRelease_Negative_BadOutcomeForeignAndMissing(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	if _, err := d.Release(context.Background(), testRef(), ReleaseRequest{Session: "s1", Outcome: "landed"}); !errors.Is(err, ErrNoClaim) {
		t.Fatalf("release without a claim: %v", err)
	}
	mustClaim(t, d, "s1")
	if _, err := d.Release(context.Background(), testRef(), ReleaseRequest{Session: "s1", Outcome: "done"}); err == nil {
		t.Fatal("an unknown outcome must be refused")
	}
	if _, err := d.Release(context.Background(), testRef(), ReleaseRequest{Session: "s2", Outcome: "landed"}); !errors.Is(err, ErrClaimHeld) {
		t.Fatalf("release by a foreign session must be refused: %v", err)
	}
	if !f.onIssue[LabelInProgress] {
		t.Fatal("a refused release must leave the labels")
	}
}

func TestClaim_Negative_ForgeErrorsFailClosed(t *testing.T) {
	for _, method := range []string{"GetIssue", "ListIssueComments", "CreateIssueComment", "EnsureLabel", "AddLabels", "Viewer", "AddAssignees"} {
		t.Run(method, func(t *testing.T) {
			f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
			f.failOn = method
			_, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "s1", Lane: "agy", Branch: "b"})
			if !errors.Is(err, ErrClaimUnverifiable) {
				t.Fatalf("%s failure must be ErrClaimUnverifiable, got %v", method, err)
			}
			if errors.Is(err, ErrClaimHeld) {
				t.Fatal("a forge error must never read as a held claim")
			}
			f.failOn = ""
			for _, c := range f.comments {
				if got, ok := ParseClaimMarker(c.Body); ok && !got.Released() {
					t.Fatalf("a failed claim must not leave a live claim comment: %s", c.Body)
				}
			}
		})
	}
}

func TestLiveClaim_Negative_ForgeErrorIsNotNoClaim(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	f.failOn = "ListIssueComments"
	holder, err := newTestDesk(f, clock).LiveClaim(context.Background(), testRef())
	if holder != nil || !errors.Is(err, ErrClaimUnverifiable) {
		t.Fatalf("an unreadable thread must be unverifiable, got %v %v", holder, err)
	}
}

func TestClaim_Negative_OpenerAndPullRequestFailClosed(t *testing.T) {
	clock := &deskClock{now: claimEpoch}
	d := &ClaimDesk{Open: func(string, string) (ClaimForge, error) { return nil, errors.New("no token") }, Stale: time.Hour, Now: clock.Now}
	if _, err := d.Claim(context.Background(), testRef(), ClaimRequest{Session: "s", Lane: "l", Branch: "b"}); !errors.Is(err, ErrClaimUnverifiable) {
		t.Fatalf("opener failure: %v", err)
	}
	f := newFakeClaimForge()
	f.pr = true
	if _, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "s", Lane: "l", Branch: "b"}); err == nil {
		t.Fatal("a pull request number must not be claimed")
	}
	bad := newTestDesk(newFakeClaimForge(), clock)
	bad.Stale = 0
	if _, err := bad.Claim(context.Background(), testRef(), ClaimRequest{Session: "s", Lane: "l", Branch: "b"}); err == nil {
		t.Fatal("a zero stale window must be refused, not defaulted")
	}
}

func TestClaim_Negative_LabelFailureAbandonsTheComment(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	f.failOn = "AddAssignees"
	_, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "s1", Lane: "l", Branch: "b"})
	if err == nil {
		t.Fatal("expected an error")
	}
	got, ok := ParseClaimMarker(f.comments[0].Body)
	if !ok || !got.Released() || got.Outcome != "abandoned" {
		t.Fatalf("half-made claim must be finalised as abandoned: %+v", got)
	}
}

func TestClaim_Negative_MalformedAndForeignMarkersIgnored(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	good, err := RenderClaimMarker(claimAt("ghost", claimEpoch))
	if err != nil {
		t.Fatal(err)
	}
	f.comments = []IssueComment{
		{ID: 1, Association: "OWNER", Body: strings.Replace(good, "v=1", "v=2", 1)},
		{ID: 2, Association: "OWNER", Body: strings.Replace(good, "stage=implementing", "stage=implementing extra=1", 1)},
		{ID: 3, Association: "OWNER", Body: strings.Replace(good, "session=ghost", "session=ghost session=twin", 1)},
		{ID: 4, Association: "OWNER", Body: "intro text\n" + good},
		{ID: 5, Association: "NONE", Body: good},
		{ID: 6, Association: "CONTRIBUTOR", Body: good},
		{ID: 7, Association: "OWNER", Body: strings.Replace(good, "session=ghost", "session=gh ost", 1)},
		{ID: 8, Association: "OWNER", Body: strings.Replace(good, "updated=2026-10-08T12:00:00Z", "updated=yesterday", 1)},
		{ID: 9, Association: "OWNER", Body: "<!-- praetor-claim"},
	}
	f.nextID = 50
	res, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "real", Lane: "l", Branch: "b"})
	if err != nil || res.Action != "created" {
		t.Fatalf("no malformed or foreign marker may hold the issue: %+v %v", res, err)
	}
}

func TestClaim_Negative_ConcurrentClaimerLosesToLowerCommentID(t *testing.T) {
	f, clock := newFakeClaimForge(), &deskClock{now: claimEpoch}
	f.beforeList = func(f *fakeClaimForge, call int) {
		if call == 2 { // the read-back after our write: a rival's older comment appears
			rival, _ := RenderClaimMarker(claimAt("rival", claimEpoch))
			f.comments = append([]IssueComment{{ID: 1, Association: "OWNER", Body: rival}}, f.comments...)
		}
	}
	_, err := newTestDesk(f, clock).Claim(context.Background(), testRef(), ClaimRequest{Session: "late", Lane: "l", Branch: "b"})
	var held *ClaimHeldError
	if !errors.As(err, &held) || held.Holder.Session != "rival" {
		t.Fatalf("the later claimer must lose naming the rival, got %v", err)
	}
	mine, _ := ParseClaimMarker(f.comments[1].Body)
	if !mine.Released() || mine.Outcome != "abandoned" {
		t.Fatalf("the loser must finalise its own comment: %+v", mine)
	}
}

func TestParseClaimRef(t *testing.T) {
	good := map[string]ClaimRef{
		"acme/widgets#7":      {Owner: "acme", Repo: "widgets", Number: 7},
		"a-b/c.d_e#123456789": {Owner: "a-b", Repo: "c.d_e", Number: 123456789},
	}
	for text, want := range good {
		got, err := ParseClaimRef(text)
		if err != nil || got != want {
			t.Fatalf("%q: got %+v err %v", text, got, err)
		}
	}
	for _, text := range []string{"", "#7", "7", "widgets#7", "acme/widgets", "acme/widgets#", "acme/widgets#0", "acme/widgets#-1",
		"acme/widgets#1234567890", "acme/widgets#7x", "../x#1", "acme/../x#1", "acme/widgets/extra#1", "acme/wid gets#1", "acme/widgets#1#2"} {
		if _, err := ParseClaimRef(text); err == nil {
			t.Fatalf("%q must be refused", text)
		}
	}
}

func TestClaimMarker_RoundTripAndBounds(t *testing.T) {
	c := claimAt("s-1", claimEpoch)
	c.Outcome = "landed"
	c.Stage = ClaimStageReleased
	marker, err := RenderClaimMarker(c)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseClaimMarker(marker + "\r\nrest")
	if !ok || got.Session != "s-1" || got.Outcome != "landed" || !got.Released() {
		t.Fatalf("round trip failed: %+v %v", got, ok)
	}
	bad := c
	bad.Branch = "has space"
	if _, err := RenderClaimMarker(bad); err == nil {
		t.Fatal("a value outside the allow-list must not render")
	}
	bad = c
	bad.Session = strings.Repeat("a", 129)
	if _, err := RenderClaimMarker(bad); err == nil {
		t.Fatal("a 129-character value must not render")
	}
	bad.Session = strings.Repeat("a", 128)
	if _, err := RenderClaimMarker(bad); err != nil {
		t.Fatalf("a 128-character value must render: %v", err)
	}
	if _, ok := ParseClaimMarker(strings.Repeat("x", MaxClaimMarkerBytes+1)); ok {
		t.Fatal("an over-long first line is never a claim")
	}
}
