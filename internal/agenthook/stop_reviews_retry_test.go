package agenthook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// stopReviewExit supplies a command exit status without starting a subprocess.
type stopReviewExit int

// Error retains the command diagnostic used by stop denials.
func (e stopReviewExit) Error() string { return fmt.Sprintf("exit status %d", e) }

// ExitCode implements the same status interface as exec.ExitError.
func (e stopReviewExit) ExitCode() int { return int(e) }

// throttledStopReviews plants failures at either read boundary and counts every try.
type throttledStopReviews struct {
	*stopReviewFixture
	inventory bool
	failures  int
	failure   error
	tries     int
}

// OwnOpen counts discovery attempts only when discovery is the planted failing phase.
func (f *throttledStopReviews) OwnOpen(ctx context.Context, repo string) ([]int, error) {
	if f.inventory {
		f.tries++
		if f.tries <= f.failures {
			return nil, f.failure
		}
	}
	return f.stopReviewFixture.OwnOpen(ctx, repo)
}

// ReadBatch counts batch attempts and delegates successful reads to complete offline snapshots.
func (f *throttledStopReviews) ReadBatch(ctx context.Context, repo string, numbers []int) (map[int]forge.ReviewSnapshot, error) {
	if !f.inventory {
		f.tries++
		if f.tries <= f.failures {
			return nil, f.failure
		}
	}
	return f.stopReviewFixture.ReadBatch(ctx, repo, numbers)
}

// installStopReviewRetryClock advances only synthetic time and restores the shared clock and sleeper.
func installStopReviewRetryClock(t *testing.T) (*time.Time, *[]time.Duration) {
	t.Helper()
	now := time.Now()
	var sleeps []time.Duration
	previousNow, previousSleep := stopReviewNow, stopReviewSleep
	stopReviewNow = func() time.Time { return now }
	stopReviewSleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		now = now.Add(delay)
		return nil
	}
	t.Cleanup(func() { stopReviewNow, stopReviewSleep = previousNow, previousSleep })
	return &now, &sleeps
}

// TestStopReviewThrottleRecovery requires a real second reader call for clean and unresolved snapshots.
func TestStopReviewThrottleRecovery(t *testing.T) {
	for _, inventory := range []bool{false, true} {
		for _, unresolved := range []bool{false, true} {
			t.Run(fmt.Sprintf("inventory=%t/unresolved=%t", inventory, unresolved), func(t *testing.T) {
				now, sleeps := installStopReviewRetryClock(t)
				reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), inventory: inventory,
					failures: 1, failure: fmt.Errorf("gh review: %w", stopReviewExit(75))}
				reader.snapshot.Threads[0].Resolved = !unresolved
				observed := *now
				got := readStopReviews(t.Context(), reader, "example/widget", *now)
				want := stopReviewVerdict("example/widget", reader.snapshot, observed)
				if got.Outcome != want.Outcome || got.Reason != want.Reason || got.Notice != "" ||
					reader.tries != 2 || len(*sleeps) != 1 || (*sleeps)[0] != 2*time.Second {
					t.Fatalf("verdict=%+v want=%+v tries=%d sleeps=%v", got, want, reader.tries, *sleeps)
				}
			})
		}
	}
}

// TestStopReviewThrottleExhaustion denies typed local throttles even inside forge rate-limit wrappers.
func TestStopReviewThrottleExhaustion(t *testing.T) {
	for _, inventory := range []bool{false, true} {
		t.Run(fmt.Sprintf("inventory=%t", inventory), func(t *testing.T) {
			now, sleeps := installStopReviewRetryClock(t)
			reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), inventory: inventory, failures: 100,
				failure: &forge.ErrRateLimited{Cause: fmt.Errorf("gh review: %w", stopReviewExit(75))}}
			got := readStopReviews(t.Context(), reader, "example/widget", *now)
			if got.Outcome != Deny || got.Notice != "" || !strings.Contains(got.Reason, "exit status 75") ||
				!strings.Contains(got.Reason, "after 2 tries") || reader.tries != 2 || len(*sleeps) != 1 {
				t.Fatalf("verdict=%+v tries=%d sleeps=%v", got, reader.tries, *sleeps)
			}
		})
	}
}

// TestStopReviewThrottleBudget refuses a retry when the enclosing stop deadline leaves no read reserve.
func TestStopReviewThrottleBudget(t *testing.T) {
	for _, budget := range []time.Duration{time.Second, 3*time.Second - time.Nanosecond} {
		for _, inventory := range []bool{false, true} {
			now, sleeps := installStopReviewRetryClock(t)
			ctx, cancel := context.WithDeadline(t.Context(), now.Add(budget))
			reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), inventory: inventory,
				failures: 100, failure: stopReviewExit(75)}
			got := readStopReviews(ctx, reader, "example/widget", *now)
			cancel()
			if got.Outcome != Deny || !strings.Contains(got.Reason, "after 1 tries") || reader.tries != 1 || len(*sleeps) != 0 {
				t.Fatalf("budget=%s verdict=%+v tries=%d sleeps=%v", budget, got, reader.tries, *sleeps)
			}
		}
	}
}

// TestStopReviewThrottleOtherErrors preserves immediate denial and GitHub's existing deferral.
func TestStopReviewThrottleOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		failure error
		outcome Outcome
	}{
		{errors.New("exit status 75"), Deny},
		{stopReviewExit(1), Deny},
		{context.DeadlineExceeded, Deny},
		{&forge.ErrRateLimited{Cause: errors.New("gh_guard: review paced (graphql 0 of 100), reset 22:45")}, Allow},
	} {
		for _, inventory := range []bool{false, true} {
			now, sleeps := installStopReviewRetryClock(t)
			reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), inventory: inventory, failures: 100, failure: tc.failure}
			got := readStopReviews(t.Context(), reader, "example/widget", *now)
			if got.Outcome != tc.outcome || reader.tries != 1 || len(*sleeps) != 0 {
				t.Fatalf("failure=%v verdict=%+v tries=%d sleeps=%v", tc.failure, got, reader.tries, *sleeps)
			}
		}
	}
}

// TestStopReviewThrottleRetryLimit pins the scalar limit and exponential schedule independently of phase caps.
func TestStopReviewThrottleRetryLimit(t *testing.T) {
	now, sleeps := installStopReviewRetryClock(t)
	ctx, cancel := context.WithDeadline(t.Context(), now.Add(time.Minute))
	defer cancel()
	tries := 0
	_, err := retryStopReviewRead(ctx, func(context.Context) (int, error) {
		tries++
		return 0, stopReviewExit(75)
	})
	if tries != 4 || fmt.Sprint(*sleeps) != "[2s 4s 8s]" || err == nil || !strings.Contains(err.Error(), "after 4 tries") {
		t.Fatalf("tries=%d sleeps=%v err=%v", tries, *sleeps, err)
	}
}

// TestStopReviewThrottleWaitBoundaries permits the exact reserve and rejects failed or late sleeps.
func TestStopReviewThrottleWaitBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget time.Duration
		late   time.Duration
		fail   bool
		want   bool
	}{
		{"exact-reserve", 3 * time.Second, 0, false, true},
		{"under-reserve", 3*time.Second - time.Nanosecond, 0, false, false},
		{"late-wakeup", 3 * time.Second, time.Nanosecond, false, false},
		{"sleep-error", 3 * time.Second, 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, _ := installStopReviewRetryClock(t)
			ctx, cancel := context.WithDeadline(t.Context(), now.Add(tc.budget))
			defer cancel()
			stopReviewSleep = func(_ context.Context, delay time.Duration) error {
				*now = now.Add(delay + tc.late)
				if tc.fail {
					return errors.New("planted sleep failure")
				}
				return nil
			}
			if got := waitStopReviewRetry(ctx, 2*time.Second); got != tc.want {
				t.Fatalf("wait=%t want=%t", got, tc.want)
			}
		})
	}
}

// TestStopReviewThrottleWaitCancellation rejects unbounded and canceled waits without sleeping.
func TestStopReviewThrottleWaitCancellation(t *testing.T) {
	_, sleeps := installStopReviewRetryClock(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitStopReviewRetry(t.Context(), time.Second) || waitStopReviewRetry(ctx, time.Second) || len(*sleeps) != 0 {
		t.Fatal("unbounded or canceled wait slept")
	}
	if err := sleepStopReviewRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep ignored cancellation: %v", err)
	}
	if err := sleepStopReviewRetry(t.Context(), 0); err != nil {
		t.Fatalf("completed sleep failed: %v", err)
	}
}

// TestStopReviewThrottleCacheCannotAllow refuses completed-cache fallback after local throttle exhaustion.
func TestStopReviewThrottleCacheCannotAllow(t *testing.T) {
	now, _ := installStopReviewRetryClock(t)
	fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
	root, path := installRateLimitedStopReviews(t, fixture)
	fixture.err = &forge.ErrRateLimited{Cause: stopReviewExit(75)}
	writeStopReviewCache(t, path, stopReviewCache{Complete: true, Repository: "example/widget", Head: "same-head",
		CheckedAt: now.Add(-5 * time.Minute), Verdict: Verdict{Outcome: Allow}})
	got := evaluateStopReviewThreads(t.Context(), root)
	if got.Outcome != Deny || got.Notice != "" || !strings.Contains(got.Reason, "exit status 75") {
		t.Fatalf("local throttle used cached allow: %+v", got)
	}
}
