// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"errors"
	"testing"
	"time"
)

var deadlineNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// Positive: with minutes left, the deadline is the margin before the binary's, far past the
// five seconds the devcontainer tests once had (#1295).
func TestContextDeadline_Positive(t *testing.T) {
	binary := deadlineNow.Add(10 * time.Minute)
	got := ContextDeadline(deadlineNow, binary, true)
	if want := binary.Add(-DeadlineMargin); !got.Equal(want) {
		t.Fatalf("deadline %s, want %s", got, want)
	}
	if left := got.Sub(deadlineNow); left <= time.Minute {
		t.Fatalf("only %s left for the work, want the binary's minutes", left)
	}
}

// Negative: without a binary deadline the work is still bounded, and a binary deadline already
// past gives a deadline that is not in the future.
func TestContextDeadline_Negative(t *testing.T) {
	if got := ContextDeadline(deadlineNow, time.Time{}, false); !got.Equal(deadlineNow.Add(NoBinaryDeadlineBound)) {
		t.Fatalf("no binary deadline gave %s, want %s after now", got, NoBinaryDeadlineBound)
	}
	if got := ContextDeadline(deadlineNow, deadlineNow.Add(-time.Second), true); got.After(deadlineNow) {
		t.Fatalf("a past binary deadline gave %s, after now %s", got, deadlineNow)
	}
}

// Boundary: at exactly twice the margin both rules give the same deadline; just under it the
// work gets half of what is left, just over it the margin is kept.
func TestContextDeadline_Boundary(t *testing.T) {
	cases := []struct {
		name string
		left time.Duration
		want time.Duration
	}{
		{"twice the margin", 2 * DeadlineMargin, DeadlineMargin},
		{"just under", 2*DeadlineMargin - 2*time.Millisecond, DeadlineMargin - time.Millisecond},
		{"just over", 2*DeadlineMargin + 2*time.Millisecond, DeadlineMargin + 2*time.Millisecond},
		{"nothing left", 0, 0},
	}
	for _, c := range cases {
		got := ContextDeadline(deadlineNow, deadlineNow.Add(c.left), true)
		if want := deadlineNow.Add(c.want); !got.Equal(want) {
			t.Errorf("%s: deadline %s after now, want %s", c.name, got.Sub(deadlineNow), c.want)
		}
	}
}

// DeadlineContext ends before the binary's deadline, is live while the test runs, and is
// cancelled when the test ends.
func TestDeadlineContext(t *testing.T) {
	var kept context.Context
	t.Run("inner", func(t *testing.T) {
		kept = DeadlineContext(t)
		deadline, ok := kept.Deadline()
		if !ok {
			t.Fatal("the context has no deadline")
		}
		if binary, hasBinary := t.Deadline(); hasBinary && !deadline.Before(binary) {
			t.Fatalf("context deadline %s is not before the binary deadline %s", deadline, binary)
		}
		if err := kept.Err(); err != nil {
			t.Fatalf("the context ended while the test runs: %v", err)
		}
	})
	if err := kept.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("after the test the context reports %v, want context.Canceled", err)
	}
}
