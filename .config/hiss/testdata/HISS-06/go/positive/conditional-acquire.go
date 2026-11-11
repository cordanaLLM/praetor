package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout after a conditional semaphore acquisition, which should be reported.
func process(string) {}

// ConditionalAcquire acquires the semaphore only when cond holds, leaving the other path unbounded.
func ConditionalAcquire(ctx context.Context, items []string, cond bool) error {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		if cond {
			if err := sem.Acquire(ctx, 1); err != nil {
				return err
			}
		}
		v := item
		go func() {
			process(v)
		}()
	}
	return nil
}
