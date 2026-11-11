package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

type fakeGate struct{}

func (fakeGate) Acquire(context.Context, int64) error { return nil }
func (fakeGate) Release(int64)                        {}

func nonLimiter() fakeGate { return fakeGate{} }

// process demonstrates multi-name spec classification, where non-limiter bindings must be reported.
func process(string) {}

// MultiNameArraySpec pairs a fixed array with a slice in one var spec and ranges over the slice.
func MultiNameArraySpec(extra []string) {
	var fixed, items = [4]string{}, extra
	_ = fixed
	for _, item := range items {
		go process(item)
	}
}

// MultiNameSemaphoreSpec pairs a real semaphore with a non-limiter in one var spec and uses the non-limiter.
func MultiNameSemaphoreSpec(ctx context.Context, items []string) error {
	var realSem, sem = semaphore.NewWeighted(4), nonLimiter()
	_ = realSem
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
