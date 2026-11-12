package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type budgetStopReviews struct {
	*delayedStopReviews
	delay  time.Duration
	budget time.Duration
}

// ReadBatch plants a slow, cancellable batch and records its allotted budget (#1894).
func (f *budgetStopReviews) ReadBatch(ctx context.Context, repo string, numbers []int) (map[int]forge.ReviewSnapshot, error) {
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return f.delayedStopReviews.ReadBatch(ctx, repo, numbers)
	}
}

// installBudgetStopReviews supplies numbered PRs, an offline identity and a fixed clock (#1894).
func installBudgetStopReviews(t *testing.T, count int, delay time.Duration) (string, *budgetStopReviews, time.Time) {
	t.Helper()
	reader := &budgetStopReviews{delayedStopReviews: &delayedStopReviews{numbers: make([]int, count)}, delay: delay}
	for i := range reader.numbers {
		reader.numbers[i] = i + 1
	}
	root := installDelayedStopReviews(t, reader.delayedStopReviews)
	stopReviewReader = func(string) forge.ReviewReader { return reader }
	now := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	previous := stopReviewNow
	stopReviewNow = func() time.Time { return now }
	t.Cleanup(func() { stopReviewNow = previous })
	return root, reader, now
}

// TestStopReviewScaledBudget admits a 42-PR live batch exceeding the old 4 s ceiling (#1894).
func TestStopReviewScaledBudget(t *testing.T) {
	root, reader, _ := installBudgetStopReviews(t, 42, 4200*time.Millisecond)
	started := time.Now()
	verdict := evaluateStopReviewThreads(t.Context(), root)
	t.Logf("42 PR live read: %s; batch budget: %s", time.Since(started), reader.budget)
	if verdict.Outcome != Allow || verdict.Notice != "" || reader.batches.Load() != 1 {
		t.Fatalf("slow live batch did not finish: %+v; batches=%d", verdict, reader.batches.Load())
	}
}

// TestStopReviewBudgetBoundaries pins the floor, per-PR increment and hard cap (#1894).
func TestStopReviewBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		count int
		want  time.Duration
	}{
		{0, 4 * time.Second}, {42, 8200 * time.Millisecond}, {80, 12 * time.Second}, {100, 12 * time.Second},
	} {
		root, reader, _ := installBudgetStopReviews(t, tc.count, 0)
		if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Allow {
			t.Fatal(verdict)
		}
		if reader.budget > tc.want || reader.budget < tc.want-200*time.Millisecond {
			t.Errorf("%d PRs: budget %s, want %s", tc.count, reader.budget, tc.want)
		}
	}
	if budgetFor(EventStop) != 54*time.Second {
		t.Fatalf("shared stop budget = %s, want 54s below launcher 60s", budgetFor(EventStop))
	}
}

// TestStopReviewOverrunCache preserves a completed result across HEAD changes, with its age (#1894).
func TestStopReviewOverrunCache(t *testing.T) {
	for _, outcome := range []Outcome{Allow, Deny} {
		for _, failure := range []error{context.DeadlineExceeded, errors.New("planted batch failure")} {
			root, _, now := installBudgetStopReviews(t, 1, 0)
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
			fixture.err = failure
			stopReviewReader = func(string) forge.ReviewReader { return fixture }
			cache := stopReviewCache{Repository: "example/widget", Head: "other-head", CheckedAt: now.Add(-5 * time.Minute),
				Verdict: Verdict{Outcome: outcome}}
			if outcome == Deny {
				cache.Verdict.Reason = "Own pull request example/widget#7: main.go:9 by review-bot"
			}
			path := filepath.Join(root, "praetor-stop-reviews.json")
			writeStopReviewCache(t, path, cache)
			verdict := evaluateStopReviewThreads(t.Context(), root)
			if verdict.Outcome != outcome || verdict.Reason != cache.Verdict.Reason ||
				!strings.Contains(verdict.Notice, "5m") || !strings.Contains(verdict.Notice, "live read") ||
				!strings.Contains(verdict.Notice, failure.Error()) {
				t.Errorf("cached %v after %v: %+v", outcome, failure, verdict)
			}
			assertStopReviewCacheUnchanged(t, path, cache)
		}
	}
}

// TestStopReviewOverrunRejectsCache pins stale, foreign, incomplete and absent cache refusals (#1894).
func TestStopReviewOverrunRejectsCache(t *testing.T) {
	for _, tc := range []rejectedStopReviewCache{
		{"absent", 5 * time.Minute, "example/widget", Allow, ""},
		{"stale", 31 * time.Minute, "example/widget", Allow, ""},
		{"exact-cap", 30 * time.Minute, "example/widget", Allow, ""},
		{"future", -time.Second, "example/widget", Allow, ""},
		{"foreign", 5 * time.Minute, "other/repository", Allow, ""},
		{"deferral", 5 * time.Minute, "example/widget", Allow, "review inventory deferred"},
		{"skip", 5 * time.Minute, "example/widget", Skip, ""},
		{"malformed", 5 * time.Minute, "example/widget", Allow, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testStopReviewOverrunRejectsCache(t, tc)
		})
	}
}

type rejectedStopReviewCache struct {
	name       string
	age        time.Duration
	repository string
	outcome    Outcome
	notice     string
}

// testStopReviewOverrunRejectsCache checks one invalid cache without hiding an unread denial (#1894).
func testStopReviewOverrunRejectsCache(t *testing.T, tc rejectedStopReviewCache) {
	t.Helper()
	root, reader, now := installBudgetStopReviews(t, 1, 0)
	reader.fail = 1
	cache := stopReviewCache{Repository: tc.repository, Head: "other-head", CheckedAt: now.Add(-tc.age),
		Verdict: Verdict{Outcome: tc.outcome, Notice: tc.notice}}
	path := filepath.Join(root, "praetor-stop-reviews.json")
	if tc.name != "absent" {
		writeStopReviewCache(t, path, cache)
	}
	if tc.name == "malformed" {
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || verdict.Notice != "" || !strings.Contains(verdict.Reason, "review state not read for #1") {
		t.Fatalf("invalid cache hid unread deny: %+v", verdict)
	}
	if tc.name == "absent" {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("incomplete cache written: %v", err)
		}
	} else if tc.name != "malformed" {
		assertStopReviewCacheUnchanged(t, path, cache)
	}
}

// TestStopReviewCacheNoticeOutput requires merged notices through every stop outcome and encoder (#1894).
func TestStopReviewCacheNoticeOutput(t *testing.T) {
	const notice = "live read overran; newest completed read is 5m0s old"
	for _, client := range []string{"claude", "codex", "gemini", "agy"} {
		for _, outcome := range []Outcome{Allow, Deny, Skip} {
			for _, active := range []bool{false, true} {
				dialect, ok := DialectFor(client)
				if !ok {
					t.Fatal(client)
				}
				verdict := mergeStopChecks(Verdict{Outcome: outcome, Reason: "other stop check"}, Verdict{Outcome: Allow, Notice: notice})
				response := dialect.Encode(Canonical{Event: EventStop, StopActive: active}, verdict)
				if !strings.Contains(string(response.Stdout)+string(response.Stderr), notice) {
					t.Errorf("%s %v active=%t dropped notice: %+v", client, outcome, active, response)
				}
			}
		}
	}
}

// assertStopReviewCacheUnchanged rejects replacement of a completed cache by an incomplete read (#1894).
func assertStopReviewCacheUnchanged(t *testing.T, path string, want stopReviewCache) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got stopReviewCache
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Repository != want.Repository || got.Head != want.Head || !got.CheckedAt.Equal(want.CheckedAt) ||
		got.Verdict.Outcome != want.Verdict.Outcome || got.Verdict.Reason != want.Verdict.Reason || got.Verdict.Notice != want.Verdict.Notice {
		t.Fatalf("incomplete read replaced cache: got %+v, want %+v", got, want)
	}
}

// TestStopReviewCacheAgeAfterOverrun rejects a cache reaching the cap during the live read (#1894).
func TestStopReviewCacheAgeAfterOverrun(t *testing.T) {
	root, reader, now := installBudgetStopReviews(t, 1, 0)
	stopReviewNow = func() time.Time { return now }
	reader.fail = 1
	reader.onRead = func() { now = now.Add(time.Minute) }
	path := filepath.Join(root, "praetor-stop-reviews.json")
	cache := stopReviewCache{Repository: "example/widget", Head: "other-head", CheckedAt: now.Add(-29 * time.Minute),
		Verdict: Verdict{Outcome: Allow}}
	writeStopReviewCache(t, path, cache)
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || verdict.Notice != "" {
		t.Fatalf("cache aged to the cap during read: %+v", verdict)
	}
	assertStopReviewCacheUnchanged(t, path, cache)
}

// TestStopReviewNewestCompletedRead takes the latest completed cache at failure, preserving its original age (#1894).
func TestStopReviewNewestCompletedRead(t *testing.T) {
	root, reader, now := installBudgetStopReviews(t, 1, 0)
	path := filepath.Join(root, "praetor-stop-reviews.json")
	reader.fail = 1
	cache := stopReviewCache{Repository: "example/widget", Head: "other-head", CheckedAt: now.Add(-5 * time.Minute),
		Verdict: Verdict{Outcome: Deny, Reason: "Own pull request example/widget#7: main.go:9 by review-bot"}}
	reader.onRead = func() { writeStopReviewCache(t, path, cache) }
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || verdict.Reason != cache.Verdict.Reason || !strings.Contains(verdict.Notice, "5m") {
		t.Fatalf("newest completed read lost: %+v", verdict)
	}
	assertStopReviewCacheUnchanged(t, path, cache)
}

// TestStopReviewLongFailureNotice keeps cache age visible even when client output bounds truncate errors (#1894).
func TestStopReviewLongFailureNotice(t *testing.T) {
	root, _, now := installBudgetStopReviews(t, 1, 0)
	path := filepath.Join(root, "praetor-stop-reviews.json")
	writeStopReviewCache(t, path, stopReviewCache{Repository: "example/widget", Head: "other-head",
		CheckedAt: now.Add(-5 * time.Minute), Verdict: Verdict{Outcome: Allow}})
	verdict := stopReviewFallback(path, "example/widget", now, Verdict{Outcome: Deny}, errors.New(strings.Repeat("planted ", 2000)))
	for _, client := range []string{"claude", "codex", "gemini", "agy"} {
		dialect, ok := DialectFor(client)
		if !ok {
			t.Fatal(client)
		}
		response := dialect.Encode(Canonical{Event: EventStop}, verdict)
		output := string(response.Stdout) + string(response.Stderr)
		if !strings.Contains(output, "newest completed read") || !strings.Contains(output, "5m") {
			t.Errorf("%s bounded notice lost cache age", client)
		}
	}
}

// TestStopTotalBudgetLeavesCheckpointFloor checks the shared stop deadline: review and state at their caps leave the
// checkpoint at least 18 s, and the checkpoint cap stays 30 s for the usual early review (#1894).
func TestStopTotalBudgetLeavesCheckpointFloor(t *testing.T) {
	worst := stopTotalBudget - (stopReviewBudget + stopReviewBatchBudgetCap + stateVerifyBudget)
	if worst < 18*time.Second {
		t.Fatalf("checkpoint floor = %s, want >= 18s", worst)
	}
	if stopCheckpointBudget != 30*time.Second {
		t.Fatalf("checkpoint cap = %s, want 30s", stopCheckpointBudget)
	}
	if stopTotalBudget >= 60*time.Second {
		t.Fatalf("stop deadline = %s, want below the launcher's 60s", stopTotalBudget)
	}
}
