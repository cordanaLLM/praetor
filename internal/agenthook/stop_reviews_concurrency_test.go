package agenthook

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type delayedStopReviews struct {
	numbers []int
	delay   time.Duration
	fail    int
	deny    int
	active  atomic.Int32
	peak    atomic.Int32
	reads   atomic.Int32
	mu      sync.Mutex
	onRead  func()
}

// OwnOpen returns the bounded inventory without contacting the forge.
func (f *delayedStopReviews) OwnOpen(context.Context, string) ([]int, error) {
	return f.numbers, nil
}

// Read models cancellable latency, tracks concurrency and plants a later denial.
func (f *delayedStopReviews) Read(ctx context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	f.reads.Add(1)
	f.mu.Lock()
	active := f.active.Add(1)
	f.peak.Store(max(f.peak.Load(), active))
	f.mu.Unlock()
	defer f.active.Add(-1)
	if f.onRead != nil {
		f.onRead()
	}
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return forge.ReviewSnapshot{}, ctx.Err()
	case <-timer.C:
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

// installDelayedStopReviews installs an offline reader and restores it after the test.
func installDelayedStopReviews(t *testing.T, fixture *delayedStopReviews) {
	t.Helper()
	previous := stopReviewReader
	stopReviewReader = func(string) forge.ReviewReader { return fixture }
	t.Cleanup(func() { stopReviewReader = previous })
}

// TestStopReviewColdCacheLatency plants a late unresolved thread beyond the serial budget.
func TestStopReviewColdCacheLatency(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := &delayedStopReviews{numbers: []int{1, 2, 3, 4}, delay: 3 * time.Second, deny: 4}
	installDelayedStopReviews(t, fixture)
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "example/widget#4") {
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
			fixture := &delayedStopReviews{numbers: make([]int, tc.count), delay: time.Millisecond, fail: tc.fail, deny: tc.deny}
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
	root := reviewRepositoryFixture(t)
	fixture := &delayedStopReviews{numbers: []int{1}, delay: time.Second, deny: 1}
	installDelayedStopReviews(t, fixture)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.onRead = cancel // Cancel after real local git identity resolution.
	first := evaluateStopReviewThreads(ctx, root)
	fixture.delay = 0
	second := evaluateStopReviewThreads(t.Context(), root)
	if first.Outcome != Skip || second.Outcome != Deny || fixture.reads.Load() != 2 {
		t.Fatalf("transient skip cached: first=%+v second=%+v reads=%d", first, second, fixture.reads.Load())
	}
}
