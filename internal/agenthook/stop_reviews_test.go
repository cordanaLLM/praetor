package agenthook

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

type stopReviewFixture struct {
	snapshot forge.ReviewSnapshot
	err      error
	reads    int
}

func (f *stopReviewFixture) OwnOpen(context.Context, string) ([]int, error) {
	return []int{7}, nil
}

func (f *stopReviewFixture) Read(context.Context, string, int) (forge.ReviewSnapshot, error) {
	f.reads++
	return f.snapshot, f.err
}

func reviewFixture() *stopReviewFixture {
	return &stopReviewFixture{snapshot: forge.ReviewSnapshot{Number: 7, State: "OPEN", Threads: []forge.ReviewThread{
		{ID: "standing", Path: "main.go", Line: 9, Author: "review-bot", CreatedAt: time.Now().Add(-time.Hour)},
	}}}
}

func installReviewFixture(t *testing.T, fixture *stopReviewFixture) {
	t.Helper()
	previous := stopReviewReader
	stopReviewReader = func(string) forge.ReviewReader { return fixture }
	t.Cleanup(func() { stopReviewReader = previous })
}

func reviewRepositoryFixture(t *testing.T) string {
	t.Helper()
	root := repository(t, true)
	for _, args := range [][]string{
		{"remote", "add", "origin", "https://github.com/example/widget.git"},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
			"commit", "--allow-empty", "-m", "fixture"},
	} {
		if _, err := util.RunGit(t.Context(), root, args...); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Planted regression: the old evaluateStop never inspected the fixture thread.
// Other checks can deny, so assert the specific review reason survives their join.
func TestStopIncludesStandingOwnReviewThread(t *testing.T) {
	root := reviewRepositoryFixture(t)
	installReviewFixture(t, reviewFixture())
	for _, client := range []string{"claude", "codex", "gemini", "agy"} {
		verdict := evaluateStop(t.Context(), stopRow(t, client), root, Canonical{}, Invocation{Getenv: noEnvironment})
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "Own pull request example/widget#7 has 1 unresolved") {
			t.Fatalf("%s stop ignored planted review thread: %+v", client, verdict)
		}
	}
}

func TestStopReviewResolvedErrorAndCache(t *testing.T) {
	for _, scenario := range []string{"unresolved", "resolved", "read-error"} {
		t.Run(scenario, func(t *testing.T) {
			root := reviewRepositoryFixture(t)
			fixture := reviewFixture()
			if scenario == "resolved" {
				fixture.snapshot.Threads[0].Resolved = true
			}
			if scenario == "read-error" {
				fixture.err = errors.New("fixture unavailable")
			}
			installReviewFixture(t, fixture)
			first := evaluateStopReviewThreads(t.Context(), root)
			second := evaluateStopReviewThreads(t.Context(), root)
			want := map[string]Outcome{"unresolved": Deny, "resolved": Allow, "read-error": Skip}[scenario]
			if first.Outcome != want || second.Outcome != first.Outcome || second.Reason != first.Reason || fixture.reads != 1 {
				t.Fatalf("first=%+v second=%+v reads=%d", first, second, fixture.reads)
			}
			if scenario == "read-error" && !strings.Contains(first.Reason, "skipped: read example/widget#7: fixture unavailable") {
				t.Fatalf("missing stated read failure: %+v", first)
			}
		})
	}
}

func TestStopReviewReadFailureSurvivesOtherDeny(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	fixture.err = errors.New("fixture read denied")
	installReviewFixture(t, fixture)
	verdict := evaluateStop(t.Context(), stopRow(t, "claude"), root, Canonical{}, Invocation{Getenv: noEnvironment})
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "review check skipped:") ||
		!strings.Contains(verdict.Reason, "fixture read denied") {
		t.Fatalf("read failure hidden by ledger or checkpoint deny: %+v", verdict)
	}
}
