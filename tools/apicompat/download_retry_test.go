// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestGateDownloadRetry runs the gate unit test battery covering bounded download retries,
// backoff, cancellation, and exceptions.
func TestGateDownloadRetry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	res, err := util.RunCommandBytes(ctx, ".", "go", util.MaxCommandOutputBytes, "test", "-v", "-tags", "apicompatgate", "./gate/...")
	if err != nil {
		t.Fatalf("gate download retry test suite failed: %v\nstdout:\n%s\nstderr:\n%s", err, res.Stdout, res.Stderr)
	}
}
