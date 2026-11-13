package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// process demonstrates split-statement semaphore acquisitions guarded by terminating err != nil checks.
func process(string) {}

// AcquireSplitShortDecl checks err != nil immediately after := acquisition before launching work.
func AcquireSplitShortDecl(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(4)
	for _, item := range items {
		err := sem.Acquire(ctx, 1)
		if err != nil {
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

// AcquireSplitPlainAssign checks err != nil immediately after = acquisition before launching work.
func AcquireSplitPlainAssign(ctx context.Context, items []string) error {
	sem := semaphore.NewWeighted(4)
	var err error
	for _, item := range items {
		err = sem.Acquire(ctx, 1)
		if err != nil {
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
