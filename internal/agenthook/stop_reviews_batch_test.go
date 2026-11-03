package agenthook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type batchStopReviews struct {
	numbers []int
	cancel  context.CancelFunc
	err     error
	batches atomic.Int32
	reads   atomic.Int32
}

// OwnOpen supplies thirty own open pull requests without contacting a forge.
func (f *batchStopReviews) OwnOpen(context.Context, string) ([]int, error) {
	return f.numbers, nil
}

// Read models 300 ms reads and deterministically exhausts the remaining budget after the first wave.
func (f *batchStopReviews) Read(ctx context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	f.reads.Add(1)
	if number > 8 && f.cancel != nil {
		f.cancel()
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return forge.ReviewSnapshot{}, ctx.Err()
	case <-timer.C:
		return batchStopSnapshot(number), nil
	}
}

// ReadBatch returns the same thread states in one call, or plants a complete batch failure.
func (f *batchStopReviews) ReadBatch(context.Context, string, []int) (map[int]forge.ReviewSnapshot, error) {
	f.batches.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	result := make(map[int]forge.ReviewSnapshot, len(f.numbers))
	for _, number := range f.numbers {
		result[number] = batchStopSnapshot(number)
	}
	return result, nil
}

// batchStopSnapshot plants unresolved threads near the start and end of the inventory.
func batchStopSnapshot(number int) forge.ReviewSnapshot {
	snapshot := forge.ReviewSnapshot{Number: number, State: "OPEN"}
	if number == 7 || number == 30 {
		snapshot.Threads = []forge.ReviewThread{{ID: "planted", Path: "batch.go", Line: number,
			Author: "review-bot", CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	}
	return snapshot
}

// thirtyStopReviews supplies the inventory shared by success and complete-failure regressions.
func thirtyStopReviews() *batchStopReviews {
	fixture := &batchStopReviews{numbers: make([]int, 30)}
	for i := range fixture.numbers {
		fixture.numbers[i] = i + 1
	}
	return fixture
}

// TestStopReviewBatchThirtyPullRequests rejects the legacy partial-read and skip path.
func TestStopReviewBatchThirtyPullRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), stopReviewBudget)
	defer cancel()
	fixture := thirtyStopReviews()
	fixture.cancel = cancel
	verdict := readStopReviews(ctx, fixture, "example/widget", time.Now())
	for _, number := range []int{7, 30} {
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, fmt.Sprintf("example/widget#%d has 1 unresolved", number)) {
			t.Errorf("unresolved #%d missing: %+v", number, verdict)
		}
	}
	if fixture.batches.Load() != 1 || fixture.reads.Load() != 0 || strings.Contains(verdict.Reason, "skipped") || strings.Contains(verdict.Reason, "not read") {
		t.Fatalf("partial legacy path: batches=%d reads=%d verdict=%+v", fixture.batches.Load(), fixture.reads.Load(), verdict)
	}
}

// TestStopReviewBatchFailureDeniesEveryUnread rejects skip-only handling of a failed batch.
func TestStopReviewBatchFailureDeniesEveryUnread(t *testing.T) {
	fixture := thirtyStopReviews()
	fixture.err = errors.New("planted batch failure")
	verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
	unread := make([]string, len(fixture.numbers))
	for i, number := range fixture.numbers {
		unread[i] = fmt.Sprintf("#%d", number)
	}
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "review state not read for "+strings.Join(unread, ", ")+" within the stop budget") ||
		!strings.Contains(verdict.Reason, "planted batch failure") || fixture.reads.Load() != 0 {
		t.Fatalf("unread PRs passed: %+v reads=%d", verdict, fixture.reads.Load())
	}
}

// TestStopReviewBatchAllClientStopEvents pins the batch path through every native stop registration.
func TestStopReviewBatchAllClientStopEvents(t *testing.T) {
	for _, tc := range []struct{ client, native string }{
		{"claude", "Stop"}, {"codex", "Stop"}, {"gemini", "AfterAgent"}, {"agy", "Stop"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			installDelayedStopReviews(t, &delayedStopReviews{})
			root := reviewRepositoryFixture(t)
			fixture := &batchStopReviews{numbers: []int{7, 30}}
			stopReviewReader = func(string) forge.ReviewReader { return fixture }
			row := stopRow(t, tc.client)
			if row.NativeEvent != tc.native {
				t.Fatalf("native stop event = %s, want %s", row.NativeEvent, tc.native)
			}
			response := serve(t, tc.client, "stop", root, commandPayload(t, map[string]any{
				"hook_event_name": tc.native, "executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true,
			}))
			output := string(response.Stdout) + string(response.Stderr)
			if fixture.batches.Load() != 1 || fixture.reads.Load() != 0 ||
				!strings.Contains(output, "example/widget#7 has 1 unresolved") || !strings.Contains(output, "example/widget#30 has 1 unresolved") {
				t.Fatalf("stop missed batch: response=%+v batches=%d reads=%d", response, fixture.batches.Load(), fixture.reads.Load())
			}
		})
	}
}

// TestStopReviewBatchMissingSnapshotDenies rejects missing or invalid snapshots without a rate-limit deferral.
func TestStopReviewBatchMissingSnapshotDenies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot forge.ReviewSnapshot
	}{
		{"absent", forge.ReviewSnapshot{}},
		{"identity", forge.ReviewSnapshot{Number: 8, State: "OPEN"}},
		{"state", forge.ReviewSnapshot{Number: 30, State: "UNKNOWN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denies := stopReviewBatchDenies("example/widget", []int{7, 30}, map[int]forge.ReviewSnapshot{
				7: batchStopSnapshot(7), 30: tc.snapshot,
			}, time.Now(), false)
			verdict := joinReviewDenies(denies)
			if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "example/widget#7 has 1 unresolved") ||
				!strings.Contains(verdict.Reason, "review state not read for #30 within the stop budget") {
				t.Fatalf("partial batch hid unread PR: %+v", verdict)
			}
		})
	}
}

// TestStopReviewIdentityBudgetDenies plants cancellation before inventory discovery, which must also fail closed.
func TestStopReviewIdentityBudgetDenies(t *testing.T) {
	root := installDelayedStopReviews(t, &delayedStopReviews{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stopReviewIdentity = func(ctx context.Context, _ string) (string, string, string, error) {
		return "", "", "", ctx.Err()
	}
	verdict := evaluateStopReviewThreads(ctx, root)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "review state not read within the stop budget") {
		t.Fatalf("identity budget exhaustion passed: %+v", verdict)
	}
}
