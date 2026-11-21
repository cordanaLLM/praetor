// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestBoundedCallPositive preserves a value and wraps the blocking call's own error with its step.
func TestBoundedCallPositive(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	cause := errors.New("disk refused")
	for _, err := range []error{nil, cause} {
		got, actual := BoundedCall(ctx, "stat directory", func() (int, error) { return 42, err }, nil)
		if got != 42 || !errors.Is(actual, err) {
			t.Fatalf("value=%d error=%v; want 42, %v", got, actual, err)
		}
		if actual != nil && !strings.Contains(actual.Error(), "stat directory") {
			t.Fatalf("step lost: %v", actual)
		}
	}
}

// TestBoundedCallNegative returns before stalled I/O is released, then disposes its late value.
func TestBoundedCallNegative(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	release, discarded := make(chan struct{}), make(chan int, 1)
	t.Cleanup(func() {
		close(release)
		select {
		case got := <-discarded:
			if got != 42 {
				t.Errorf("late value = %d", got)
			}
		case <-time.After(time.Second):
			t.Error("late value was not disposed")
		}
	})
	start := time.Now()
	_, err := BoundedCall(ctx, "sync file", func() (int, error) {
		<-release
		return 42, nil
	}, func(value int) { discarded <- value })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "sync file") {
		t.Fatalf("deadline error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("stalled call delayed caller by %s", elapsed)
	}
	// Cleanup releases the underlying call only after this deadline assertion.
	select {
	case <-discarded:
		t.Fatal("stalled call finished before release")
	default:
	}
}

// TestBoundedCallBoundary refuses unbounded/pre-cancelled work and proves late delivery workers exit.
func TestBoundedCallBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	cancel()
	called := false
	call := func() (int, error) { called = true; return 0, nil }
	for _, input := range []context.Context{nil, context.WithoutCancel(t.Context()), ctx} {
		if _, err := BoundedCall(input, "open", call, nil); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	if called {
		t.Fatal("refused operation ran")
	}
	if _, err := BoundedCall[int](ctx, "open", nil, nil); err == nil {
		t.Fatal("nil call accepted")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		deliverBoundedResult(ctx, make(chan boundedResult[int]), func() (int, error) { return 7, nil }, nil)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled worker leaked at result delivery")
	}
}

// handoffDeadlineContext lets the deadline expire during the caller's second Err check.
type handoffDeadlineContext struct {
	context.Context
	checked atomic.Bool
}

// Err waits for the deadline only after the preflight check has passed.
func (c *handoffDeadlineContext) Err() error {
	if c.checked.Swap(true) {
		<-c.Done()
	}
	return c.Context.Err()
}

// TestBoundedCallDeadlineAtHandoff proves cleanup cannot delay the deadline return.
func TestBoundedCallDeadlineAtHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		handoff := &handoffDeadlineContext{Context: ctx}
		result := make(chan boundedResult[int], 1)
		discarded, release := make(chan int, 1), make(chan struct{})
		defer close(release)
		go func(ctx context.Context) {
			value, err := BoundedCall(ctx, "open", func() (int, error) {
				return 42, nil
			}, func(value int) {
				discarded <- value
				<-release
			})
			result <- boundedResult[int]{value, err}
		}(handoff)
		select {
		case got := <-result:
			if got.value != 0 || !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatalf("value=%d error=%v; want zero and deadline exceeded", got.value, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("deadline return blocked on cleanup")
		}
		select {
		case value := <-discarded:
			if value != 42 {
				t.Fatalf("discarded value = %d; want 42", value)
			}
		case <-time.After(time.Second):
			t.Fatal("late value was not disposed")
		}
	})
}
