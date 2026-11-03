package agenthook

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

type stopReviewFixture struct {
	snapshot  forge.ReviewSnapshot
	snapshots map[int]forge.ReviewSnapshot
	numbers   []int
	err       error
	reads     atomic.Int32
}

// OwnOpen returns the configured fixture pull request numbers.
func (f *stopReviewFixture) OwnOpen(context.Context, string) ([]int, error) {
	if len(f.numbers) > 0 {
		return f.numbers, nil
	}
	return []int{7}, nil
}

// Read increments the read counter and returns the configured mock review snapshot or error.
func (f *stopReviewFixture) Read(_ context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	f.reads.Add(1)
	if snapshot, ok := f.snapshots[number]; ok {
		return snapshot, f.err
	}
	return f.snapshot, f.err
}

// ReadBatch supplies configured complete snapshots and retains read counts for cache assertions.
func (f *stopReviewFixture) ReadBatch(ctx context.Context, repo string, numbers []int) (map[int]forge.ReviewSnapshot, error) {
	result := make(map[int]forge.ReviewSnapshot, len(numbers))
	for _, number := range numbers {
		snapshot, err := f.Read(ctx, repo, number)
		if err != nil {
			return result, err
		}
		result[number] = snapshot
	}
	return result, nil
}

// reviewFixture constructs a mock review fixture containing one unresolved review thread.
func reviewFixture() *stopReviewFixture {
	return &stopReviewFixture{snapshot: forge.ReviewSnapshot{Number: 7, State: "OPEN", Threads: []forge.ReviewThread{
		{ID: "standing", Path: "main.go", Line: 9, Author: "review-bot", CreatedAt: time.Now().Add(-time.Hour)},
	}}}
}

// installReviewFixture swaps the package stopReviewReader function for testing and restores it on cleanup.
func installReviewFixture(t *testing.T, fixture *stopReviewFixture) {
	t.Helper()
	previous := stopReviewReader
	stopReviewReader = func(string) forge.ReviewReader { return fixture }
	t.Cleanup(func() { stopReviewReader = previous })
}

// reviewRepositoryFixture initializes an empty git repository configured with an origin remote for testing.
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

// Planted regression: the old evaluateStop never inspected the fixture thread, and agy Stop
// bypassed evaluateStopReviewThreads in dispatch. Assert dispatch and agy Run surface the
// unresolved thread's path, line and author.
func TestStopIncludesStandingOwnReviewThread(t *testing.T) {
	for _, client := range []string{"agy", "claude", "codex", "gemini"} {
		root := reviewRepositoryFixture(t)
		installReviewFixture(t, reviewFixture())
		verdict := dispatch(t.Context(), stopRow(t, client), Canonical{Event: EventStop}, root, Invocation{Getenv: noEnvironment})
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "Own pull request example/widget#7 has 1 unresolved") ||
			!strings.Contains(verdict.Reason, "main.go:9 by review-bot") {
			t.Fatalf("%s stop ignored planted review thread: %+v", client, verdict)
		}
	}
	root := reviewRepositoryFixture(t)
	installReviewFixture(t, reviewFixture())
	first := serve(t, "agy", "stop", root, commandPayload(t, map[string]any{"executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true}))
	second := serve(t, "agy", "stop", root, commandPayload(t, map[string]any{"executionNum": 2, "terminationReason": "model_stop", "fullyIdle": true}))
	if agyStdoutAllows(t, "stop", first) || !strings.Contains(string(first.Stdout), "main.go:9 by review-bot") || !agyStdoutAllows(t, "stop", second) {
		t.Fatalf("agy stop review routing: first=%s second=%s", first.Stdout, second.Stdout)
	}
}

// TestStopReviewMultiplePullRequestsAndThreads pins deny reporting across multiple open pull requests and unresolved threads.
func TestStopReviewMultiplePullRequestsAndThreads(t *testing.T) {
	root := reviewRepositoryFixture(t)
	now := time.Now()
	installReviewFixture(t, &stopReviewFixture{
		numbers: []int{7, 8},
		snapshots: map[int]forge.ReviewSnapshot{
			7: {Number: 7, State: "OPEN", Threads: []forge.ReviewThread{
				{ID: "t1", Path: "main.go", Line: 9, Author: "review-bot", CreatedAt: now.Add(-time.Hour)},
				{ID: "t2", Path: "cli.go", Line: 23, Author: "sourcery-ai", CreatedAt: now.Add(-2 * time.Hour)},
				{ID: "t3", Path: "ignored.go", Line: 1, Author: "done", Resolved: true, CreatedAt: now.Add(-3 * time.Hour)},
			}},
			8: {Number: 8, State: "OPEN", Threads: []forge.ReviewThread{
				{ID: "t4", Path: "stop.go", Line: 38, Author: "coderabbitai", CreatedAt: now.Add(-30 * time.Minute)},
			}},
		},
	})
	verdict := evaluateStopReviewThreads(t.Context(), root)
	for _, want := range []string{
		"example/widget#7 has 2 unresolved review thread(s)",
		"main.go:9 by review-bot, cli.go:23 by sourcery-ai",
		"Also failing: Own pull request example/widget#8 has 1 unresolved review thread(s)",
		"stop.go:38 by coderabbitai",
	} {
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, want) {
			t.Fatalf("missing %q in %+v", want, verdict)
		}
	}
	if strings.Contains(verdict.Reason, "ignored.go") {
		t.Fatalf("resolved thread leaked into deny: %+v", verdict)
	}
}

// TestStopReviewResolvedErrorAndCache pins evaluation outcomes, error handling, and cache behavior across resolved states.
func TestStopReviewResolvedErrorAndCache(t *testing.T) {
	for _, tc := range []struct {
		name          string
		resolved      bool
		err           error
		first, second Outcome
		reads         int
		reason        string
	}{
		{"unresolved", false, nil, Deny, Allow, 2, "main.go:9 by review-bot"},
		{"resolved", true, nil, Allow, Allow, 1, ""},
		{"read-error", false, errors.New("fixture unavailable"), Deny, Deny, 2, "review state not read for #7 within the stop budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := reviewRepositoryFixture(t)
			fixture := reviewFixture()
			fixture.snapshot.Threads[0].Resolved = tc.resolved
			fixture.err = tc.err
			installReviewFixture(t, fixture)
			first := evaluateStopReviewThreads(t.Context(), root)
			fixture.snapshot.Threads[0].Resolved = true
			second := evaluateStopReviewThreads(t.Context(), root)
			if first.Outcome != tc.first || second.Outcome != tc.second || fixture.reads.Load() != int32(tc.reads) || !strings.Contains(first.Reason, tc.reason) {
				t.Fatalf("first=%+v second=%+v reads=%d", first, second, fixture.reads.Load())
			}
		})
	}
}

// TestStopReviewReadFailureSurvivesOtherDeny pins that unread PRs remain denials alongside other stop checks.
func TestStopReviewReadFailureSurvivesOtherDeny(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	fixture.err = errors.New("fixture read denied")
	installReviewFixture(t, fixture)
	verdict := evaluateStop(t.Context(), stopRow(t, "claude"), root, Canonical{}, Invocation{Getenv: noEnvironment})
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "review state not read for #7 within the stop budget") ||
		!strings.Contains(verdict.Reason, "fixture read denied") {
		t.Fatalf("read failure hidden by ledger or checkpoint deny: %+v", verdict)
	}
}
