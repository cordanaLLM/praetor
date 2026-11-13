package p

import "golang.org/x/sync/semaphore"

type statusRecorder struct{}

func (statusRecorder) Fatal(string) {}

// process demonstrates fanout when the semaphore failure branch calls a non-terminating Fatal method.
func process(string) {}

// FatalNonTesting calls a custom type's Fatal method on TryAcquire failure and continues to launch.
func FatalNonTesting(items []string, rec statusRecorder) {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		if !sem.TryAcquire(1) {
			rec.Fatal("saturated")
		}
		v := item
		go func() {
			defer sem.Release(1)
			process(v)
		}()
	}
}
