// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"fmt"
)

// boundedResult transfers a blocking call's value and error together, never through shared state.
type boundedResult[T any] struct {
	value T
	err   error
}

// BoundedCall bounds the caller's wait for blocking I/O, which a Go context cannot interrupt
// in the kernel. A deadline is required. discard releases a late value (such as an opened
// handle); nil is suitable for values needing no cleanup. The worker drains its own result
// on cancellation, so it never remains blocked sending after the underlying call finishes.
// A permanently stalled kernel call can still retain its worker until the OS releases it.
func BoundedCall[T any](ctx context.Context, step string, call func() (T, error), discard func(T)) (T, error) {
	var zero T
	if ctx == nil || call == nil {
		return zero, fmt.Errorf("%s: context and blocking call required", step)
	}
	if _, ok := ctx.Deadline(); !ok {
		return zero, fmt.Errorf("%s: context deadline required", step)
	}
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("%s: %w", step, err)
	}
	result := make(chan boundedResult[T])
	go deliverBoundedResult(ctx, result, call, discard)
	select {
	case got := <-result:
		if err := ctx.Err(); err != nil {
			if discard != nil {
				go discard(got.value)
			}
			return zero, fmt.Errorf("%s: %w", step, err)
		}
		if got.err != nil {
			return got.value, fmt.Errorf("%s: %w", step, got.err)
		}
		return got.value, nil
	case <-ctx.Done():
		return zero, fmt.Errorf("%s: %w", step, ctx.Err())
	}
}

// deliverBoundedResult owns late values; the unbuffered handoff cannot abandon a resource
// between observing cancellation and sending it to a caller that has already returned.
func deliverBoundedResult[T any](ctx context.Context, result chan<- boundedResult[T], call func() (T, error), discard func(T)) {
	value, err := call()
	select {
	case result <- boundedResult[T]{value, err}:
	case <-ctx.Done():
		if discard != nil {
			discard(value)
		}
	}
}
