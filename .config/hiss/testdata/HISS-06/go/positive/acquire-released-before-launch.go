package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout after releasing the acquired permit prior to launching work.
func process(string) {}

// AcquireReleasedBeforeLaunch releases the semaphore permit before starting the goroutine.
func AcquireReleasedBeforeLaunch(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		sem.Release(1)
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
	return nil
}
