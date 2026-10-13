// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"testing"
	"time"
)

// DeadlineMargin is how long before the test binary's own deadline (go test -timeout) a
// DeadlineContext ends, so that a test whose work runs out of time fails with its own message
// instead of the binary's timeout panic.
const DeadlineMargin = 10 * time.Second

// NoBinaryDeadlineBound bounds a DeadlineContext when the test binary has no deadline
// (go test -timeout 0): go test's default -timeout, so the work stays bounded (HISS-02).
const NoBinaryDeadlineBound = 10 * time.Minute

// DeadlineContext returns a context for a test's own work that ends with the test (its cleanup
// cancels it) or shortly before the test binary's deadline, whichever comes first
// (ContextDeadline). A fixed deadline of a few seconds turned host load into failures of tests
// whose work was correct (#1295); this one ends only when the binary itself is about to run
// out of time.
func DeadlineContext(t *testing.T) context.Context {
	t.Helper()
	binary, ok := t.Deadline()
	ctx, cancel := context.WithDeadline(context.Background(), ContextDeadline(time.Now(), binary, ok))
	t.Cleanup(cancel)
	return ctx
}

// ContextDeadline returns the deadline DeadlineContext gives at now: DeadlineMargin before the
// binary deadline, half of what is left when that is at most twice the margin, and
// NoBinaryDeadlineBound after now when the binary has no deadline (ok false).
func ContextDeadline(now, binary time.Time, ok bool) time.Time {
	if !ok {
		return now.Add(NoBinaryDeadlineBound)
	}
	left := binary.Sub(now)
	if left <= 2*DeadlineMargin {
		return now.Add(left / 2)
	}
	return binary.Add(-DeadlineMargin)
}
