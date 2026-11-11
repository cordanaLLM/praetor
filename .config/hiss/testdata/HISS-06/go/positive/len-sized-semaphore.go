package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout with an input-sized weighted semaphore, which bounds nothing.
func process(string) {}

// LenSizedSemaphore sizes the semaphore by len(items), allowing one goroutine per input item.
func LenSizedSemaphore(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(int64(len(items)))
	for _, item := range items {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
	return nil
}
