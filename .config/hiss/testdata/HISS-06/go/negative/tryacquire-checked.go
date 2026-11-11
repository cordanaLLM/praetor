package p

import "golang.org/x/sync/semaphore"

// process demonstrates a guarded TryAcquire worker bound, which should be accepted.
func process(string) {}

// BoundedByTryAcquire skips iterations where TryAcquire fails and releases the permit on exit.
func BoundedByTryAcquire(items []string) {
	sem := semaphore.NewWeighted(8)
	for _, item := range items {
		if !sem.TryAcquire(1) {
			continue
		}
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
