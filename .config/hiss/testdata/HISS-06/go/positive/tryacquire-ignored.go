package p

import "golang.org/x/sync/semaphore"

// process demonstrates fanout after an unchecked TryAcquire call, which should be reported.
func process(string) {}

// TryAcquireIgnored drops the boolean result of TryAcquire and launches a goroutine regardless.
func TryAcquireIgnored(items []string) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		sem.TryAcquire(1)
		v := item
		go func() {
			process(v)
		}()
	}
}
