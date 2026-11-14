package agenthook

import (
	"context"
	"fmt"
	"time"
)

// stopReviewRetryLimit bounds local-throttle reads to one initial try and three retries.
// The 1 s, 2 s, 4 s waits plus a 1 s reserve permit three tries in 4 s inventory and four in 12 s batches, if reads leave time.
const stopReviewRetryLimit = 4

// stopReviewRetryReserve leaves at least one second for the next read within its existing phase budget.
const stopReviewRetryReserve = time.Second

// stopReviewSleep permits offline tests to advance the clock without sleeping.
var stopReviewSleep = sleepStopReviewRetry

// stopReviewLocallyThrottled recognizes gh_guard's command status through wrapped and joined errors.
func stopReviewLocallyThrottled(err error) bool {
	throttled := false
	complete := walkStopReviewReadErrors(err, func(read stopReviewReadError) {
		throttled = throttled || stopReviewLocalThrottle(read)
	})
	return throttled || !complete
}

// stopReviewLocalThrottle recognizes exit 75 without parsed GitHub budget metadata on that leaf's path.
func stopReviewLocalThrottle(read stopReviewReadError) bool {
	exit, ok := read.err.(interface{ ExitCode() int })
	return ok && exit.ExitCode() == 75 && (read.limited == nil || read.limited.Bucket == "")
}

// retryStopReviewRead retries only local throttles within the original discovery or batch deadline.
func retryStopReviewRead[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	var value T
	var err error
	var tries int
	for tries = 1; tries <= stopReviewRetryLimit; tries++ {
		value, err = read(ctx)
		if !stopReviewLocallyThrottled(err) {
			return value, err
		}
		if tries == stopReviewRetryLimit || !waitStopReviewRetry(ctx, time.Second<<(tries-1)) {
			break
		}
	}
	return value, fmt.Errorf("%w (after %d tries)", err, tries)
}

// waitStopReviewRetry reserves read time before waiting and checks it again after a possibly late wakeup.
func waitStopReviewRetry(ctx context.Context, delay time.Duration) bool {
	deadline, bounded := ctx.Deadline()
	if !bounded || ctx.Err() != nil || stopReviewNow().Add(delay+stopReviewRetryReserve).After(deadline) {
		return false
	}
	if err := stopReviewSleep(ctx, delay); err != nil {
		return false
	}
	return ctx.Err() == nil && !stopReviewNow().Add(stopReviewRetryReserve).After(deadline)
}

// sleepStopReviewRetry waits for backoff or cancellation without extending the stop deadline.
func sleepStopReviewRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
