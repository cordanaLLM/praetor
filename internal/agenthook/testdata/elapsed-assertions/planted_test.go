// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"testing"
	"time"
)

// TestPlantedElapsedBound is parsed as a negative fixture, never executed.
func TestPlantedElapsedBound(t *testing.T) {
	start := time.Now()
	if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
		t.Fatalf("took %s, want < 700ms", elapsed)
	}
}
