package p

import "golang.org/x/sync/semaphore"

// process demonstrates fanout under TryAcquire with an unproven variable weight, which may be zero.
func process(string) {}

// NonConstWeight passes a caller parameter n to TryAcquire, allowing n == 0 to bypass the limit.
func NonConstWeight(items []string, n int64) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		if !sem.TryAcquire(n) {
			continue
		}
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
