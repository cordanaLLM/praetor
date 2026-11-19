// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package admission

import "context"

// memoryBound reports no memory bound because this build lacks the Linux cgroup memory.max inspection
// path.
func memoryBound(context.Context) string { return "no memory bound applies to this step" }
