package wishes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func policy() *Policy {
	return &Policy{MaxWishes: 4, MaxPolls: 4, MaxVoters: 4, AllowVoteChanges: true, AllowWithdrawal: true}
}
func initLedger(t *testing.T) (string, *Ledger) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".workingdir", "wishes.json")
	got, err := Apply(t.Context(), path, Request{Action: "init", Policy: policy()})
	if err != nil {
		t.Fatal(err)
	}
	return path, got
}
func rev(n uint64) *uint64 { return &n }
func wishReq() Request {
	return Request{Action: "add-wish", ExpectedRevision: rev(1), Wish: &WishDraft{ID: "w1", Kind: "template", Target: "repo", Title: "Add template", Description: "useful template"}}
}

func TestLifecycleAndCAS(t *testing.T) {
	path, _ := initLedger(t)
	ledger, err := Apply(t.Context(), path, wishReq())
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Revision != 2 || ledger.Wishes[0].Status != "proposed" {
		t.Fatalf("unexpected ledger: %+v", ledger)
	}
	if _, err := Apply(t.Context(), path, wishReq()); err == nil {
		t.Fatal("stale revision accepted")
	}
	_, err = Apply(t.Context(), path, Request{Action: "open-poll", ExpectedRevision: rev(2), Poll: &PollDraft{ID: "p1", WishID: "w1", Question: "Accept?", Choices: []Choice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = Apply(t.Context(), path, Request{Action: "vote", ExpectedRevision: rev(3), PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Wishes[0].Status != "proposed" {
		t.Fatal("vote autoaccepted wish")
	}
	if _, err := Apply(t.Context(), path, Request{Action: "close-poll", ExpectedRevision: rev(4), PollID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), path, Request{Action: "vote", ExpectedRevision: rev(5), PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}}); err == nil {
		t.Fatal("closed poll accepted vote")
	}
}

func TestStrictDecodeAndBoundaries(t *testing.T) {
	for _, raw := range []string{`{"action":"init","policy":{},"typo":true}`, `{"action":"init","policy":{}} {}`, `{"action":"init","policy":{"max_wishes":1,"max_polls":1,"max_voters":1,"allow_vote_changes":true,"allow_withdrawal":true},"action":"init"}`} {
		if _, err := DecodeRequest([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed request %s", raw)
		}
	}
	path, _ := initLedger(t)
	if _, err := Read(t.Context(), path+"-missing"); err == nil {
		t.Fatal("missing ledger accepted")
	}
	if err := os.Symlink(path, path+"-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(t.Context(), path+"-link"); err == nil {
		t.Fatal("symlink ledger accepted")
	}
}

func TestVoteAndWithdrawalPolicy(t *testing.T) {
	path, _ := initLedger(t)
	if _, err := Apply(t.Context(), path, wishReq()); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), path, Request{Action: "open-poll", ExpectedRevision: rev(2), Poll: &PollDraft{ID: "p1", WishID: "w1", Question: "Choose", Choices: []Choice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), path, Request{Action: "vote", ExpectedRevision: rev(3), PollID: "p1", ActorID: "external:a", ChoiceIDs: []string{"yes"}}); err == nil {
		t.Fatal("non-local actor accepted")
	}
	if _, err := Apply(t.Context(), path, Request{Action: "vote", ExpectedRevision: rev(3), PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), path, Request{Action: "withdraw", ExpectedRevision: rev(4), PollID: "p1", ActorID: "local:a"}); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(context.Background(), path); err != nil || len(got.CurrentBallots) != 0 {
		t.Fatalf("withdraw readback: %+v %v", got, err)
	}
}
