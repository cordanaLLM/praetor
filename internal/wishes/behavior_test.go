package wishes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func applyCurrent(t *testing.T, path string, request Request) *Ledger {
	t.Helper()
	current, err := Read(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = &current.Revision
	ledger, err := Apply(t.Context(), path, request)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func pollFixture(t *testing.T, p *Policy) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wishes.json")
	if _, err := Apply(t.Context(), path, Request{Action: "init", Policy: p}); err != nil {
		t.Fatal(err)
	}
	applyCurrent(t, path, Request{Action: "add-wish", Wish: &WishDraft{
		ID: "w1", Kind: "framework", Target: "fixture/repo", Title: "A useful framework", Description: "Wünsche for a shared framework"}})
	applyCurrent(t, path, Request{Action: "open-poll", Poll: &PollDraft{ID: "p1", WishID: "w1",
		Question: "Prioritize this wish?", Choices: []Choice{{ID: "yes", Label: "Yes"}, {ID: "later", Label: "Later"}}}})
	return path
}

func TestVotesRemainIndependentAcrossPolls(t *testing.T) {
	path := pollFixture(t, nil)
	applyCurrent(t, path, Request{Action: "open-poll", Poll: &PollDraft{ID: "p2", WishID: "w1",
		Question: "Reuse existing package?", Choices: []Choice{{ID: "yes", Label: "Yes"}, {ID: "later", Label: "Later"}}}})
	vote := Request{Action: "vote", PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}}
	applyCurrent(t, path, vote)
	vote.PollID = "p2"
	ledger := applyCurrent(t, path, vote)
	if len(ledger.CurrentBallots) != 2 {
		t.Fatal("one poll overwrote another poll's ballot")
	}
	vote.ChoiceIDs = []string{"later"}
	ledger = applyCurrent(t, path, vote)
	if ledger.Polls[0].Tallies[0].Votes != 1 || ledger.Polls[1].Tallies[1].Votes != 1 {
		t.Fatalf("wrong per-poll tally after vote change: %+v", ledger.Polls)
	}
	ledger = applyCurrent(t, path, Request{Action: "withdraw", PollID: "p2", ActorID: "local:a"})
	if len(ledger.CurrentBallots) != 1 || ledger.Polls[1].Tallies[1].Votes != 0 || ledger.Wishes[0].Status != "proposed" {
		t.Fatalf("withdrawal changed unrelated state: %+v", ledger)
	}
	ledger = applyCurrent(t, path, Request{Action: "close-poll", PollID: "p1"})
	if _, err := Apply(t.Context(), path, Request{Action: "withdraw", PollID: "p1", ActorID: "local:a", ExpectedRevision: &ledger.Revision}); err == nil {
		t.Fatal("closed poll accepted withdrawal")
	}
}

func TestVotePolicyRejectsChangesWithoutWrites(t *testing.T) {
	p := &Policy{MaxWishes: 1, MaxPolls: 1, MaxVoters: 1}
	path := pollFixture(t, p)
	ledger := applyCurrent(t, path, Request{Action: "vote", PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Action: "vote", PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"later"}},
		{Action: "vote", PollID: "p1", ActorID: "local:b", ChoiceIDs: []string{"yes"}},
		{Action: "withdraw", PollID: "p1", ActorID: "local:a"},
		{Action: "open-poll", Poll: &PollDraft{ID: "p2", WishID: "w1", Question: "Extra?", Choices: ledger.Polls[0].Choices}},
		{Action: "add-wish", Wish: &WishDraft{ID: "w2", Kind: "skill", Target: "repo", Title: "Extra", Description: "Extra wish"}},
	} {
		request.ExpectedRevision = &ledger.Revision
		if _, err := Apply(t.Context(), path, request); err == nil {
			t.Fatalf("configured bound or policy ignored for %s", request.Action)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed operation changed ledger: %v", err)
		}
	}
}

func TestConcurrentWritersCannotLoseAcceptedWish(t *testing.T) {
	path, initial := initLedger(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Go(func() {
			<-start
			_, err := Apply(t.Context(), path, Request{Action: "add-wish", ExpectedRevision: &initial.Revision,
				Wish: &WishDraft{ID: fmt.Sprintf("w%d", i), Kind: "skill", Target: "repo", Title: "Concurrent wish", Description: "Keep accepted wish"}})
			results <- err
		})
	}
	close(start)
	group.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	ledger, err := Read(t.Context(), path)
	if err != nil || accepted != 1 || len(ledger.Wishes) != 1 || ledger.Revision != initial.Revision+1 {
		t.Fatalf("concurrent writes lost state: accepted=%d ledger=%+v err=%v", accepted, ledger, err)
	}
}

func TestCanceledOperationsAndCorruptTallyFail(t *testing.T) {
	path := pollFixture(t, nil)
	ledger := applyCurrent(t, path, Request{Action: "vote", PollID: "p1", ActorID: "local:a", ChoiceIDs: []string{"yes"}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Read(ctx, path); err == nil {
		t.Fatal("canceled read accepted")
	}
	if _, err := Apply(ctx, path, Request{Action: "close-poll", PollID: "p1", ExpectedRevision: &ledger.Revision}); err == nil {
		t.Fatal("canceled mutation accepted")
	}
	ledger.Polls[0].Tallies[0].Votes = 100
	corrupt, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(t.Context(), path); err == nil {
		t.Fatal("tampered tally accepted")
	}
}
