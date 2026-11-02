package agenthook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type delayedStopReviews struct {
	numbers  []int
	inFlight int32
	ready    chan struct{}
	released bool
	fail     int
	deny     int
	active   atomic.Int32
	peak     atomic.Int32
	reads    atomic.Int32
	mu       sync.Mutex
	onRead   func()
}

// OwnOpen returns the bounded inventory without contacting the forge.
func (f *delayedStopReviews) OwnOpen(context.Context, string) ([]int, error) {
	return f.numbers, nil
}

// enterRead records an in-flight read and returns the barrier channel when one is configured.
func (f *delayedStopReviews) enterRead() <-chan struct{} {
	f.reads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	active := f.active.Add(1)
	f.peak.Store(max(f.peak.Load(), active))
	if f.inFlight <= 0 {
		return nil
	}
	if f.ready == nil {
		f.ready = make(chan struct{})
	}
	if active >= f.inFlight && !f.released {
		f.released = true
		close(f.ready)
	}
	return f.ready
}

// Read blocks until the configured in-flight count is reached or ctx ends, then returns the snapshot.
func (f *delayedStopReviews) Read(ctx context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	ready := f.enterRead()
	defer f.active.Add(-1)
	if f.onRead != nil {
		f.onRead()
	}
	if ready != nil {
		select {
		case <-ctx.Done():
			return forge.ReviewSnapshot{}, ctx.Err()
		case <-ready:
		}
	}
	if err := ctx.Err(); err != nil {
		return forge.ReviewSnapshot{}, err
	}
	if number == f.fail {
		return forge.ReviewSnapshot{}, errors.New("planted read failure")
	}
	snapshot := forge.ReviewSnapshot{Number: number, State: "OPEN"}
	if number == f.deny {
		snapshot.Threads = []forge.ReviewThread{{ID: "late", Path: "late.go", Line: 9, CreatedAt: time.Now()}}
	}
	return snapshot, nil
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

// TestStopReviewColdCacheLatency requires all four cold-cache reads to be in flight concurrently to reach the last pull request.
func TestStopReviewColdCacheLatency(t *testing.T) {
	fixture := &delayedStopReviews{numbers: []int{1, 2, 3, 4}, inFlight: 4, deny: 4}
	root := installDelayedStopReviews(t, fixture)
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "example/widget#4") ||
		fixture.reads.Load() != 4 || fixture.peak.Load() != 4 {
		t.Fatalf("cold cache missed late unresolved thread: %+v; reads=%d peak=%d", verdict, fixture.reads.Load(), fixture.peak.Load())
	}
	if fixture.active.Load() != 0 {
		t.Fatal("review workers outlived evaluation")
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
		{"overflow", 101, 0, 0, Skip},
		{"error-before-deny", 4, 1, 4, Deny},
		{"error-only", 4, 1, 0, Skip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &delayedStopReviews{numbers: make([]int, tc.count), fail: tc.fail, deny: tc.deny}
			for i := range fixture.numbers {
				fixture.numbers[i] = i + 1
			}
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != tc.want || fixture.active.Load() != 0 || fixture.peak.Load() > 8 {
				t.Fatalf("verdict=%+v active=%d peak=%d", verdict, fixture.active.Load(), fixture.peak.Load())
			}
			if tc.count > forge.MaxReviewPullRequests && fixture.reads.Load() != 0 {
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
	if first.Outcome != Skip || second.Outcome != Deny || fixture.reads.Load() != 2 {
		t.Fatalf("transient skip cached: first=%+v second=%+v reads=%d", first, second, fixture.reads.Load())
	}
}
