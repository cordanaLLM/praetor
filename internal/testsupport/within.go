// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"testing"
	"time"
)

// RunWithin runs fn and returns its error, failing the test when fn has not returned after
// limit. A reader that opens a FIFO blocks in open(2), which no context can interrupt, so a
// regression would otherwise hang the whole test binary instead of failing one test.
func RunWithin(t testing.TB, limit time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("call did not return within %s", limit)
		return nil
	}
}
