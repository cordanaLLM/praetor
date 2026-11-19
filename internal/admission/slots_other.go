// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package admission

import (
	"context"
	"io"
	"runtime"
)

// HeavyCountAvailable reports whether this build has kernel-held heavy slots.
const HeavyCountAvailable = false

// acquire admits uncounted with an explicit platform diagnostic because this build lacks kernel-held
// heavy-slot support. A canceled context is still refused.
func (s Slots) acquire(ctx context.Context, _ string, _ io.Writer) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Lease{Text: "heavy-step count is not available on " + runtime.GOOS + "; admitted uncounted"}, nil
}
