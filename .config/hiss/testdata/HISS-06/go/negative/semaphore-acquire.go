package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

func process(string) {}

// BoundedBySemaphore acquires a weighted semaphore inside the loop before launching each goroutine.
func BoundedBySemaphore(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(8)
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
