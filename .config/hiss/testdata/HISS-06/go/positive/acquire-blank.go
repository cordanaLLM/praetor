package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout when Acquire's error is discarded into the blank identifier.
func process(string) {}

// AcquireBlank discards the error from Acquire and launches a goroutine without holding a permit.
func AcquireBlank(ctx context.Context, items []string) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		_ = sem.Acquire(ctx, 1)
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
