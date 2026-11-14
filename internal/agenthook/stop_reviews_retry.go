package agenthook

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// stopReviewRetryLimit bounds local-throttle reads to one initial try and three retries.
const stopReviewRetryLimit = 4

// stopReviewRetryReserve leaves at least one second for the next read within its existing phase budget.
const stopReviewRetryReserve = time.Second

// stopReviewSleep permits offline tests to advance the clock without sleeping.
var stopReviewSleep = sleepStopReviewRetry

// stopReviewLocallyThrottled recognizes gh_guard's command status through wrapped and joined errors.
func stopReviewLocallyThrottled(err error) bool {
	var exit interface{ ExitCode() int }
	return errors.As(err, &exit) && exit.ExitCode() == 75
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
		if tries == stopReviewRetryLimit || !waitStopReviewRetry(ctx, time.Second<<tries) {
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
