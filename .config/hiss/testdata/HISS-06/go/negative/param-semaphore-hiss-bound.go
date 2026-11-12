package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates fanout bounded by a caller-supplied semaphore documented with //hiss:bound.
func process(string) {}

// BoundedParamSemaphore accepts a *semaphore.Weighted parameter whose declaring line carries a
// //hiss:bound directive stating the caller-enforced capacity.
func BoundedParamSemaphore(
	ctx context.Context,
	sem *semaphore.Weighted, //hiss:bound sem<=8 caller constructs pool semaphore with bounded weight
	items []string,
) error {
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
