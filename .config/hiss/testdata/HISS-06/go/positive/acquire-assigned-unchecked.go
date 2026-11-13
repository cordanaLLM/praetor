package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// recordErr consumes an error without terminating the loop iteration.
func recordErr(error) {}

// process demonstrates fanout when Acquire's assigned error is not guarded by if err != nil.
func process(string) {}

// AcquireAssignedUnchecked assigns Acquire's error without a terminating err != nil guard.
func AcquireAssignedUnchecked(ctx context.Context, items []string) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		err := sem.Acquire(ctx, 1)
		recordErr(err)
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
