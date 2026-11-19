// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"fmt"
)

// UnscopedBounds names the enclosing memory bound and retained concurrency compensation.
// Failure to establish a memory bound is reported, never used as a refusal or a silent bound.
func UnscopedBounds(ctx context.Context, label, concurrency string) string {
	if concurrency == "" {
		concurrency = "heavy-step count not requested"
	}
	return fmt.Sprintf("admission %s: %s; concurrency: %s", label, memoryBound(ctx), concurrency)
}
