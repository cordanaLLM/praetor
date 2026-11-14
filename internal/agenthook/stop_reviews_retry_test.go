package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
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
	batches   []stopReviewRetryBatch
}

// stopReviewRetryBatch supplies one attempt's snapshots and failure.
type stopReviewRetryBatch struct {
	snapshots map[int]forge.ReviewSnapshot
	err       error
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
		if f.tries <= len(f.batches) {
			batch := f.batches[f.tries-1]
			return batch.snapshots, batch.err
		}
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
					reader.tries != 2 || len(*sleeps) != 1 || (*sleeps)[0] != time.Second {
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
				!strings.Contains(got.Reason, "after 3 tries") || reader.tries != 3 || len(*sleeps) != 2 {
				t.Fatalf("verdict=%+v tries=%d sleeps=%v", got, reader.tries, *sleeps)
			}
		})
	}
}

// TestStopReviewThrottleBudget refuses a retry when the enclosing stop deadline leaves no read reserve.
func TestStopReviewThrottleBudget(t *testing.T) {
	for _, budget := range []time.Duration{time.Second, 2*time.Second - time.Nanosecond} {
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
		notice  string
	}{
		{errors.New("exit status 75"), Deny, ""},
		{stopReviewExit(1), Deny, ""},
		{context.DeadlineExceeded, Deny, ""},
		{&forge.ErrRateLimited{Cause: errors.New("gh_guard: review paced (graphql 0 of 100), reset 22:45")}, Allow,
			"review inventory deferred: GitHub rate limited: gh_guard: review paced (graphql 0 of 100), reset 22:45"},
	} {
		for _, inventory := range []bool{false, true} {
			now, sleeps := installStopReviewRetryClock(t)
			reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), inventory: inventory, failures: 100, failure: tc.failure}
			got := readStopReviews(t.Context(), reader, "example/widget", *now)
			if got.Outcome != tc.outcome || got.Notice != tc.notice || reader.tries != 1 || len(*sleeps) != 0 {
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
	if tries != 4 || fmt.Sprint(*sleeps) != "[1s 2s 4s]" || err == nil || !strings.Contains(err.Error(), "after 4 tries") {
		t.Fatalf("tries=%d sleeps=%v err=%v", tries, *sleeps, err)
	}
}

// TestStopReviewJoinedThrottle denies cached allows when an ordinary exit precedes a local throttle.
func TestStopReviewJoinedThrottle(t *testing.T) {
	now, sleeps := installStopReviewRetryClock(t)
	reader := &throttledStopReviews{stopReviewFixture: reviewFixture(), failures: 100,
		failure: errors.Join(stopReviewExit(1), &forge.ErrRateLimited{Cause: stopReviewExit(75)})}
	root, path := installRateLimitedStopReviews(t, reader)
	writeStopReviewCache(t, path, stopReviewCache{Complete: true, Repository: "example/widget", Head: "same-head",
		CheckedAt: now.Add(-5 * time.Minute), Verdict: Verdict{Outcome: Allow}})
	got := evaluateStopReviewThreads(t.Context(), root)
	if got.Outcome != Deny || got.Notice != "" || reader.tries != 3 || len(*sleeps) != 2 ||
		!strings.Contains(got.Reason, "exit status 75") {
		t.Fatalf("joined throttle used cached allow: %+v tries=%d sleeps=%v", got, reader.tries, *sleeps)
	}
}

// TestStopReviewThrottleWalkBound fails closed only when the bounded walk leaves unchecked nodes.
func TestStopReviewThrottleWalkBound(t *testing.T) {
	var failure error = stopReviewExit(1)
	for range maxStopReviewReadErrors - 1 {
		failure = fmt.Errorf("wrapped: %w", failure)
	}
	if stopReviewLocallyThrottled(failure) {
		t.Fatal("exact-bound ordinary error treated as throttle")
	}
	if !stopReviewLocallyThrottled(fmt.Errorf("overflow: %w", failure)) {
		t.Fatal("unchecked error allowed past walk bound")
	}
	failures := make([]error, maxStopReviewReadErrors+1)
	for i := range failures {
		failures[i] = stopReviewExit(1)
	}
	if !stopReviewLocallyThrottled(errors.Join(failures...)) {
		t.Fatal("unchecked joined errors allowed past width bound")
	}
}

// TestStopReviewThrottleSnapshots retains earlier threads across deferrals and overwrites resolved snapshots.
func TestStopReviewThrottleSnapshots(t *testing.T) {
	for _, mode := range []string{"github-limited", "ordinary-failure", "resolved"} {
		t.Run(mode, func(t *testing.T) { testStopReviewThrottleSnapshots(t, mode) })
	}
}

// testStopReviewThrottleSnapshots plants a partial first read followed by an empty failure or resolution.
func testStopReviewThrottleSnapshots(t *testing.T, mode string) {
	t.Helper()
	now, _ := installStopReviewRetryClock(t)
	fixture := reviewFixture()
	fixture.numbers = []int{7, 8}
	later := stopReviewRetryBatch{err: &forge.ErrRateLimited{Bucket: "graphql", Remaining: "0", Limit: "100", Reset: "22:45"}}
	if mode == "ordinary-failure" {
		later.err = errors.New("planted later failure")
	}
	if mode == "resolved" {
		resolved := fixture.snapshot
		resolved.Threads = nil
		later = stopReviewRetryBatch{snapshots: map[int]forge.ReviewSnapshot{7: resolved, 8: {Number: 8, State: "OPEN"}}}
	}
	reader := &throttledStopReviews{stopReviewFixture: fixture, batches: []stopReviewRetryBatch{
		{snapshots: map[int]forge.ReviewSnapshot{7: fixture.snapshot}, err: fmt.Errorf("read example/widget#8: %w", stopReviewExit(75))}, later,
	}}
	root, path := installRateLimitedStopReviews(t, reader)
	writeStopReviewCache(t, path, stopReviewCache{Complete: true, Repository: "example/widget", Head: "same-head",
		CheckedAt: now.Add(-5 * time.Minute), Verdict: Verdict{Outcome: Allow}})
	got := evaluateStopReviewThreads(t.Context(), root)
	if reader.tries != 2 {
		t.Fatalf("tries=%d want=2", reader.tries)
	}
	if mode == "resolved" {
		if got.Outcome != Allow || got.Reason != "" || got.Notice != "" {
			t.Fatalf("resolved thread retained: %+v", got)
		}
		return
	}
	assertStopReviewRetrySnapshotDeny(t, got, mode)
}

// assertStopReviewRetrySnapshotDeny requires a retained thread and any GitHub deferral notice.
func assertStopReviewRetrySnapshotDeny(t *testing.T, got Verdict, mode string) {
	t.Helper()
	if got.Outcome != Deny || !strings.Contains(got.Reason, "example/widget#7 has 1 unresolved") ||
		!strings.Contains(got.Reason, "main.go:9 by review-bot") {
		t.Fatalf("earlier thread lost: %+v", got)
	}
	if mode == "github-limited" && got.Notice != "review inventory deferred: graphql 0 of 100, reset 22:45" {
		t.Fatalf("deferral notice lost: %+v", got)
	}
}

// TestStopReviewThrottlePhaseTries pins the documented attempt counts under real phase budget values.
func TestStopReviewThrottlePhaseTries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget time.Duration
		tries  int
		sleeps string
	}{
		{"inventory", stopReviewBudget, 3, "[1s 2s]"},
		{"largest-batch", stopReviewBatchBudget(forge.MaxReviewPullRequests), 4, "[1s 2s 4s]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, sleeps := installStopReviewRetryClock(t)
			ctx, cancel := context.WithDeadline(t.Context(), now.Add(tc.budget))
			defer cancel()
			tries := 0
			_, err := retryStopReviewRead(ctx, func(context.Context) (int, error) {
				tries++
				return 0, stopReviewExit(75)
			})
			if tries != tc.tries || fmt.Sprint(*sleeps) != tc.sleeps || err == nil ||
				!strings.Contains(err.Error(), fmt.Sprintf("after %d tries", tc.tries)) {
				t.Fatalf("tries=%d sleeps=%v err=%v", tries, *sleeps, err)
			}
		})
	}
}

// TestStopReviewThrottleCommandFixture emits only a planted pacing diagnostic from a child test process.
func TestStopReviewThrottleCommandFixture(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--stop-review-throttle" {
		return
	}
	if _, err := fmt.Fprint(os.Stderr, os.Args[len(os.Args)-1]); err != nil {
		t.Fatal(err)
	}
	os.Exit(75)
}

// stopReviewThrottleCommand supplies an offline gh replacement with a real exit status and stderr.
func stopReviewThrottleCommand(t *testing.T, diagnostic string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, dir string, _ ...string) ([]byte, error) {
		result, err := util.RunCommandBytes(ctx, dir, executable, util.MaxCommandOutputBytes,
			"-test.run=^TestStopReviewThrottleCommandFixture$", "--", "--stop-review-throttle", diagnostic)
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exit.Stderr = result.Stderr
		}
		return result.Stdout, err
	}
}

// TestStopReviewThrottleBudgetMetadata distinguishes GitHub-budget pacing from the local minute cap.
func TestStopReviewThrottleBudgetMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, diagnostic string
		outcome          Outcome
		tries            int
		notice           string
	}{
		{"github-budget", "gh_guard: review paced (graphql 0 of 100), reset 22:45", Allow, 1,
			"review inventory deferred: graphql 0 of 100, reset 22:45"},
		{"local-cap", "uncached calls reached 100/60s across callers", Deny, 3, ""},
		{"unclassified", "", Deny, 3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, sleeps := installStopReviewRetryClock(t)
			command := stopReviewThrottleCommand(t, tc.diagnostic)
			calls := 0
			reader := forge.GHReviewReader{Dir: t.TempDir(), Run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
				calls++
				return command(ctx, dir, args...)
			}}
			got := readStopReviews(t.Context(), reader, "example/widget", *now)
			if got.Outcome != tc.outcome || got.Notice != tc.notice || calls != tc.tries || len(*sleeps) != tc.tries-1 {
				t.Fatalf("verdict=%+v calls=%d sleeps=%v", got, calls, *sleeps)
			}
		})
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
