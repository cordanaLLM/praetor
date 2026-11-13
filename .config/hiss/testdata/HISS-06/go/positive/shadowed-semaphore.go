package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

type localGate struct{}

func (localGate) Acquire(context.Context, int64) error { return nil }

// process demonstrates fanout under a shadowed semaphore binding, which should be reported.
func process(string) {}

// ShadowedSemaphore shadows an outer semaphore.Weighted with a non-limiting binding before the loop.
func ShadowedSemaphore(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(4)
	_ = sem
	{
		sem := localGate{}
		for _, item := range items {
			if err := sem.Acquire(ctx, 1); err != nil {
				return err
			}
			go process(item)
		}
	}
	return nil
}
