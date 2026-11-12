package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout with a caller-supplied semaphore lacking a //hiss:bound proof.
func process(string) {}

// UnboundedParamSemaphore accepts a *semaphore.Weighted parameter without a //hiss:bound
// directive on its declaring line, leaving its capacity unproven.
func UnboundedParamSemaphore(ctx context.Context, sem *semaphore.Weighted, items []string) error {
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
