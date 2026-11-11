package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout after an unchecked semaphore.Weighted.Acquire expression statement.
func process(string) {}

// AcquireUnchecked ignores the error returned by Acquire and launches a goroutine even when
// context cancellation aborts the acquisition without obtaining a permit.
func AcquireUnchecked(ctx context.Context, items []string) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		sem.Acquire(ctx, 1)
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
