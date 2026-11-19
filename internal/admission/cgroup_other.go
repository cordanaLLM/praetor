// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package admission

import "context"

func memoryBound(context.Context) string { return "no memory bound applies to this step" }
