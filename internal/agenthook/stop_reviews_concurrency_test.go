package agenthook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type delayedStopReviews struct {
	numbers    []int
	fail, deny int
	batches    atomic.Int32
	onRead     func()
}

// OwnOpen returns the bounded inventory without contacting the forge.
func (f *delayedStopReviews) OwnOpen(context.Context, string) ([]int, error) {
	return f.numbers, nil
}

// Read rejects legacy per-PR reads in the stop path.
func (f *delayedStopReviews) Read(context.Context, string, int) (forge.ReviewSnapshot, error) {
	return forge.ReviewSnapshot{}, errors.New("unexpected per-PR read")
}

// ReadBatch supplies complete snapshots, or plants cancellation and individual unread PRs.
func (f *delayedStopReviews) ReadBatch(ctx context.Context, _ string, numbers []int) (map[int]forge.ReviewSnapshot, error) {
	f.batches.Add(1)
	if f.onRead != nil {
		f.onRead()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[int]forge.ReviewSnapshot, len(numbers))
	var failure error
	for _, number := range numbers {
		if number == f.fail {
			failure = errors.New("planted read failure")
			continue
		}
		snapshot := forge.ReviewSnapshot{Number: number, State: "OPEN"}
		if number == f.deny {
			snapshot.Threads = []forge.ReviewThread{{ID: "late", Path: "late.go", Line: 9, CreatedAt: time.Now()}}
		}
		result[number] = snapshot
	}
	return result, failure
}

// installDelayedStopReviews installs an offline reader and synthetic repository identity and restores them after the test.
func installDelayedStopReviews(t *testing.T, fixture *delayedStopReviews) string {
	t.Helper()
	root := t.TempDir()
	previousReader := stopReviewReader
	previousIdentity := stopReviewIdentity
	stopReviewReader = func(string) forge.ReviewReader { return fixture }
	stopReviewIdentity = func(_ context.Context, dir string) (string, string, string, error) {
		return "example/widget", "fixture-head", filepath.Join(dir, "praetor-stop-reviews.json"), nil
	}
	t.Cleanup(func() {
		stopReviewReader = previousReader
		stopReviewIdentity = previousIdentity
	})
	return root
}

// TestStopReviewColdCacheLatency requires a cold cache to read all four pull requests through one batch.
func TestStopReviewColdCacheLatency(t *testing.T) {
	fixture := &delayedStopReviews{numbers: []int{1, 2, 3, 4}, deny: 4}
	root := installDelayedStopReviews(t, fixture)
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "example/widget#4") ||
		fixture.batches.Load() != 1 {
		t.Fatalf("cold cache missed late unresolved thread: %+v; batches=%d", verdict, fixture.batches.Load())
	}
}

// TestStopReviewReadBoundaries pins inventory limits and preserves denials after read errors.
func TestStopReviewReadBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count, fail, deny int
		want              Outcome
	}{
		{"empty", 0, 0, 0, Allow},
		{"cap", 100, 0, 0, Allow},
		{"overflow", 101, 0, 0, Deny},
		{"error-before-deny", 4, 1, 4, Deny},
		{"error-only", 4, 1, 0, Deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &delayedStopReviews{numbers: make([]int, tc.count), fail: tc.fail, deny: tc.deny}
			for i := range fixture.numbers {
				fixture.numbers[i] = i + 1
			}
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != tc.want {
				t.Fatalf("verdict=%+v", verdict)
			}
			if tc.count > forge.MaxReviewPullRequests && fixture.batches.Load() != 0 {
				t.Fatal("overflow inventory reached reader")
			}
			if tc.fail != 0 && !strings.Contains(verdict.Reason, "planted read failure") {
				t.Fatalf("read error hidden: %+v", verdict)
			}
		})
	}
}

// TestStopReviewTimeoutRetriesSameHead plants a transient deadline followed by a reachable denial.
func TestStopReviewTimeoutRetriesSameHead(t *testing.T) {
	fixture := &delayedStopReviews{numbers: []int{1}, deny: 1}
	root := installDelayedStopReviews(t, fixture)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.onRead = cancel
	first := evaluateStopReviewThreads(ctx, root)
	fixture.onRead = nil
	second := evaluateStopReviewThreads(t.Context(), root)
	if first.Outcome != Deny || second.Outcome != Deny || fixture.batches.Load() != 2 {
		t.Fatalf("transient failure cached: first=%+v second=%+v batches=%d", first, second, fixture.batches.Load())
	}
}
